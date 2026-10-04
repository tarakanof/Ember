package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// knobNowHeader carries the server's Unix time on every view answer, 304s
// included, so the knob can count down to ends_at without a clock of its own.
const knobNowHeader = "X-Ember-Now"

// knobView is GET /v1/devices/self/view: everything the knob shows, per poll.
// Field order is the wire order. Nothing in it moves with the clock alone
// (a counting Pomodoro has an absolute ends_at instead of remaining_sec), so
// an unchanged view keeps its ETag. pomo is null with the Pomodoro off,
// weather null when disabled or never fetched.
type knobView struct {
	V             int          `json:"v"`
	Epoch         uint64       `json:"epoch"`
	ConfigVersion int          `json:"config_version"`
	Mood          knobMood     `json:"mood"`
	Pomo          *knobPomo    `json:"pomo"`
	Weather       *knobWeather `json:"weather"`
	Brightness    knobLight    `json:"brightness"`
}

type knobMood struct {
	Waiting int    `json:"waiting"`
	Errors  int    `json:"errors"`
	Running int    `json:"running"`
	Done    int    `json:"done"`
	Source  string `json:"source"`
}

type knobPomo struct {
	Phase        string `json:"phase"`
	Running      bool   `json:"running"`
	Paused       bool   `json:"paused"`
	RemainingSec *int   `json:"remaining_sec,omitempty"`
	EndsAt       *int64 `json:"ends_at,omitempty"`
	PlannedSec   int    `json:"planned_sec"`
	Round        int    `json:"round"`
}

type knobWeather struct {
	Provider string  `json:"provider"`
	Cond     string  `json:"cond"`
	Code     string  `json:"code"`
	TempC    float64 `json:"temp_c"`
	Stale    bool    `json:"stale"`
	Severe   bool    `json:"severe"`
	Sunrise  *int64  `json:"sunrise"`
	Sunset   *int64  `json:"sunset"`
}

type knobLight struct {
	Level int  `json:"level"`
	Night bool `json:"night"`
}

// knobView encodes device id's view at now and its strong ETag. It reads only
// in-memory state: no store write, no clock probe, no session marshal.
func (a *App) knobView(id string, now time.Time) ([]byte, string, error) {
	epoch, version, err := a.devices.versions(id)
	if err != nil {
		return nil, "", err
	}
	r := a.legacyRender(a.sessions.View())
	b := a.currentBrightness(now)
	v := knobView{
		V:             1,
		Epoch:         epoch,
		ConfigVersion: version,
		Mood:          knobMood{Waiting: r.Waiting, Errors: r.Errors, Running: r.Running, Done: r.Done, Source: r.Source},
		Pomo:          a.knobPomo(now),
		Weather:       a.knobWeather(now),
		Brightness:    knobLight{Level: b.Level, Night: b.Night},
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, "", fmt.Errorf("encode knob view: %w", err)
	}
	h := fnv.New64a()
	_, _ = h.Write(body)
	return body, fmt.Sprintf(`"%016x"`, h.Sum64()), nil
}

func (a *App) knobPomo(now time.Time) *knobPomo {
	if !a.pomodoroOn() {
		return nil
	}
	st, end, counting := a.engine.Snapshot(now)
	p := &knobPomo{
		Phase:      string(st.Phase),
		Running:    st.Running,
		Paused:     st.Paused,
		PlannedSec: st.PlannedSec,
		Round:      st.Round,
	}
	if counting {
		t := end.Unix()
		p.EndsAt = &t
	} else {
		rem := st.RemainingSec
		p.RemainingSec = &rem
	}
	return p
}

func (a *App) knobWeather(now time.Time) *knobWeather {
	cfg := a.cfg.Load().Weather
	obs, ok := a.weather.current()
	if !ok || !cfg.Enabled {
		return nil
	}
	w := &knobWeather{
		Provider: cfg.Provider,
		Cond:     obs.Condition,
		Code:     obs.ConditionCode,
		TempC:    obs.TempC,
		Stale:    now.Sub(obs.FetchedAt) >= weatherTileStaleTTL,
		Severe:   obs.Severe,
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		if rise, set, ok := sunTimes(cfg.Latitude, cfg.Longitude, now); ok {
			r, s := rise.Round(sunRounding).Unix(), set.Round(sunRounding).Unix()
			w.Sunrise, w.Sunset = &r, &s
		}
	}
	return w
}

func (a *App) handleDeviceSelfView(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	body, etag, err := a.knobView(deviceIDFrom(r.Context()), now)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-cache")
	h.Set(knobNowHeader, strconv.FormatInt(now.Unix(), 10))
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// etagMatches applies If-None-Match's weak comparison (RFC 9110 13.1.2).
func etagMatches(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}
