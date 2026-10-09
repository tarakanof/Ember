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

const knobNowHeader = "X-Ember-Now"

type knobView struct {
	V             int                    `json:"v"`
	Epoch         uint64                 `json:"epoch"`
	ConfigVersion int                    `json:"config_version"`
	Mood          viewBlock[knobMood]    `json:"mood,omitzero"`
	Pomo          viewBlock[knobPomo]    `json:"pomo,omitzero"`
	Weather       viewBlock[knobWeather] `json:"weather,omitzero"`
	Brightness    knobLight              `json:"brightness"`
	Quiet         bool                   `json:"quiet,omitempty"`
	NowPlaying    *knobNowPlaying        `json:"nowplaying,omitempty"`
	DiagLiveUntil *int64                 `json:"diag_live_until,omitempty"`
}

type knobMood struct {
	Waiting   int    `json:"waiting"`
	Errors    int    `json:"errors"`
	Running   int    `json:"running"`
	Done      int    `json:"done"`
	Source    string `json:"source"`
	Lead      string `json:"lead,omitempty"`
	Hosts     int    `json:"hosts,omitempty"`
	LeadColor string `json:"lead_color,omitempty"`
	Tool      string `json:"tool,omitempty"`
}

func newKnobMood(r Render, l moodLead) knobMood {
	m := knobMood{Waiting: r.Waiting, Errors: r.Errors, Running: r.Running, Done: r.Done, Source: r.Source,
		LeadColor: l.Color, Tool: l.Tool}
	if !strings.EqualFold(l.Lead, r.Source) {
		m.Lead = l.Lead
	}
	if l.Hosts > 1 {
		m.Hosts = l.Hosts
	}
	return m
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
	Night    bool    `json:"night"`
	Sunrise  *int64  `json:"sunrise"`
	Sunset   *int64  `json:"sunset"`
}

type knobNowPlaying struct {
	State      string `json:"state"`
	Source     string `json:"source,omitempty"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	TrackID    string `json:"track_id,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
	PositionAt int64  `json:"position_at,omitempty"`
	ArtVersion string `json:"art_version,omitempty"`
	AlbumArt   bool   `json:"album_art,omitempty"`
	ArtistArt  bool   `json:"artist_art,omitempty"`
	Volume     *int   `json:"volume,omitempty"`
}

const knobNowPlayingPage = "nowplaying"

type knobLight struct {
	Level int  `json:"level"`
	Night bool `json:"night"`
}

type viewBlock[T any] struct {
	on  bool
	val *T
}

func block[T any](val *T) viewBlock[T] { return viewBlock[T]{on: true, val: val} }

func (b viewBlock[T]) IsZero() bool { return !b.on }

func (b viewBlock[T]) MarshalJSON() ([]byte, error) { return json.Marshal(b.val) }

func (a *App) knobView(id string, now time.Time) ([]byte, string, error) {
	st, err := a.devices.viewState(id)
	if err != nil {
		return nil, "", err
	}
	mood := a.moodState()
	m := newKnobMood(mood.Render, mood.Lead)
	b := a.currentBrightness(now)
	v := knobView{
		V:             1,
		Epoch:         st.epoch,
		ConfigVersion: st.version,
		Mood:          block(&m),
		Pomo:          block(a.knobPomo(now)),
		Weather:       block(a.knobWeather(now)),
		Brightness:    knobLight{Level: b.Level, Night: b.Night},
		Quiet:         a.cfg.Load().quietAt(now),
		DiagLiveUntil: a.knobLiveUnix(id, st.cfg.Diagnostics, now),
	}
	if st.cfg.pageOn(knobNowPlayingPage) {
		v.NowPlaying = a.knobNowPlaying(now)
	}
	if st.caps != nil {
		v.applyCaps(st.caps, st.cfg)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, "", fmt.Errorf("encode knob view: %w", err)
	}
	if st.caps != nil {
		body, err = v.fit(body, st.caps.limits().ViewBytes)
		if err != nil {
			return nil, "", err
		}
	}
	h := fnv.New64a()
	_, _ = h.Write(body)
	return body, fmt.Sprintf(`"%016x"`, h.Sum64()), nil
}

func (v *knobView) applyCaps(caps *deviceCaps, cfg knobSettings) {
	v.V = caps.viewMajor()
	show := func(page string) bool { return caps.hasPage(page) && cfg.pageOn(page) }
	v.Mood.on = show("bot")
	v.Pomo.on = show("pomodoro")
	v.Weather.on = show("weather")
	if !show(knobNowPlayingPage) {
		v.NowPlaying = nil
	}
}

func (v *knobView) fit(body []byte, limit int) ([]byte, error) {
	drops := []func(){
		func() { v.NowPlaying = nil },
		func() { v.Weather.on = false },
		func() { v.Pomo.on = false },
	}
	for _, drop := range drops {
		if limit <= 0 || len(body) <= limit {
			break
		}
		drop()
		var err error
		if body, err = json.Marshal(v); err != nil {
			return nil, fmt.Errorf("encode knob view: %w", err)
		}
	}
	return body, nil
}

func (a *App) knobNowPlaying(now time.Time) *knobNowPlaying {
	e, ok := a.nowPlaying.reg.Current(now)
	if !ok {
		return &knobNowPlaying{State: nowPlayingNone}
	}
	return &knobNowPlaying{
		State:      string(e.State),
		Source:     e.Source,
		Title:      e.Title,
		Artist:     e.Artist,
		Album:      e.Album,
		TrackID:    e.TrackID,
		DurationMS: e.DurationMS,
		PositionMS: e.PositionMS,
		PositionAt: e.PositionAt.UnixMilli(),
		ArtVersion: e.ArtVersion(),
		AlbumArt:   e.AlbumArt != nil,
		ArtistArt:  e.ArtistArt != nil,
		Volume:     e.Volume,
	}
}

func (a *App) knobPomo(now time.Time) *knobPomo {
	ps := a.pomodoroState(now)
	if !ps.On {
		return nil
	}
	st := ps.Status
	p := &knobPomo{
		Phase:      string(st.Phase),
		Running:    st.Running,
		Paused:     st.Paused,
		PlannedSec: st.PlannedSec,
		Round:      st.Round,
	}
	if ps.Counting {
		t := ps.EndsAt.Unix()
		p.EndsAt = &t
	} else {
		rem := st.RemainingSec
		p.RemainingSec = &rem
	}
	return p
}

func (a *App) knobWeather(now time.Time) *knobWeather {
	cfg := a.cfg.Load().Weather
	wx := a.weather.state(cfg, now)
	if !wx.HaveObs || !wx.Enabled {
		return nil
	}
	w := &knobWeather{
		Provider: cfg.Provider,
		Cond:     wx.Obs.Condition,
		Code:     wx.Obs.ConditionCode,
		TempC:    wx.Obs.TempC,
		Stale:    !wx.Fresh,
		Severe:   wx.Obs.Severe,
		Night:    wx.Night,
	}
	if wx.HaveSun {
		rise, set := wx.roundedSun()
		r, s := rise.Unix(), set.Unix()
		w.Sunrise, w.Sunset = &r, &s
	}
	return w
}

func (a *App) handleDeviceSelfView(w http.ResponseWriter, r *http.Request) {
	id := deviceIDFrom(r.Context())
	wait, err := parseViewWait(r.URL.Query().Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set(knobViewWaitHeader, strconv.Itoa(int(knobViewWaitMax/time.Second)))
	if a.serveKnobViewWait(w, r, id, wait) {
		return
	}
	now := a.viewNow()
	body, etag, err := a.knobView(id, now)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.writeKnobView(w, r, body, etag, now)
}

func (a *App) writeKnobView(w http.ResponseWriter, r *http.Request, body []byte, etag string, now time.Time) {
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

func etagMatches(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}
