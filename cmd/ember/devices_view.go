package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
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
	Usage         *knobUsage             `json:"usage,omitempty"`
	DiagLiveUntil *int64                 `json:"diag_live_until,omitempty"`
	Demo          *knobDemo              `json:"demo,omitempty"`
}

type knobMood struct {
	Waiting     int    `json:"waiting"`
	Errors      int    `json:"errors"`
	Running     int    `json:"running"`
	Done        int    `json:"done"`
	Source      string `json:"source"`
	Lead        string `json:"lead,omitempty"`
	Hosts       int    `json:"hosts,omitempty"`
	LeadColor   string `json:"lead_color,omitempty"`
	Tool        string `json:"tool,omitempty"`
	LeadProject string `json:"lead_project,omitempty"`
}

func newKnobMood(r Render, l moodLead) knobMood {
	m := knobMood{Waiting: r.Waiting, Errors: r.Errors, Running: r.Running, Done: r.Done, Source: r.Source,
		LeadColor: l.Color, Tool: l.Tool, LeadProject: l.Project}
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
	Provider   string  `json:"provider"`
	Model      string  `json:"model,omitempty"`
	Cond       string  `json:"cond"`
	Code       string  `json:"code"`
	TempC      float64 `json:"temp_c"`
	Stale      bool    `json:"stale"`
	Severe     bool    `json:"severe"`
	Night      bool    `json:"night"`
	Sunrise    *int64  `json:"sunrise"`
	Sunset     *int64  `json:"sunset"`
	ObservedAt *int64  `json:"observed_at,omitempty"`
	*knobWeatherDetail
}

type knobNowPlaying struct {
	State        string `json:"state"`
	Source       string `json:"source,omitempty"`
	Title        string `json:"title,omitempty"`
	Artist       string `json:"artist,omitempty"`
	Album        string `json:"album,omitempty"`
	TrackID      string `json:"track_id,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	PositionMS   int64  `json:"position_ms,omitempty"`
	PositionAt   int64  `json:"position_at,omitempty"`
	ArtVersion   string `json:"art_version,omitempty"`
	AlbumArt     bool   `json:"album_art,omitempty"`
	ArtistArt    bool   `json:"artist_art,omitempty"`
	Volume       *int   `json:"volume,omitempty"`
	Accent       string `json:"accent,omitempty"`
	CoverVersion string `json:"cover_version,omitempty"`
	CoverLight   bool   `json:"cover_light,omitempty"`
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
	body, etag, _, err := a.buildKnobView(id, now)
	return body, etag, err
}

func (a *App) buildKnobView(id string, now time.Time) ([]byte, string, string, error) {
	st, err := a.devices.viewState(id)
	if err != nil {
		return nil, "", "", err
	}
	mood := a.moodState()
	m := newKnobMood(mood.Render, mood.Lead)
	b := a.currentBrightness(now)
	detail := st.caps.hasFeature(featureWxFaces) && st.caps.hasPage("weather") && st.cfg.pageOn("weather")
	v := knobView{
		V:             1,
		Epoch:         st.epoch,
		ConfigVersion: st.version,
		Mood:          block(&m),
		Pomo:          block(a.knobPomo(now)),
		Weather:       block(a.knobWeatherFor(now, detail)),
		Brightness:    knobLight{Level: b.Level, Night: b.Night},
		Quiet:         a.cfg.Load().quietAt(now),
		DiagLiveUntil: a.knobLiveUnix(id, st.cfg.Diagnostics, now),
	}
	if st.cfg.pageOn(knobNowPlayingPage) {
		v.NowPlaying = a.knobNowPlaying(now, st.caps.hasFeature(featureNPCover))
	}
	if st.caps != nil {
		v.applyCaps(st.caps, st.cfg)
	}
	if st.caps.hasFeature(featureUsage) {
		v.Usage = a.knobUsage(now)
	}
	if st.caps.hasFeature(featureDemo) {
		v.Demo = a.knobDemo(id, v, now)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, "", "", fmt.Errorf("encode knob view: %w", err)
	}
	if st.caps != nil {
		limit := st.caps.limits().ViewBytes
		if body, err = v.fit(body, limit); err != nil {
			return nil, "", "", err
		}
		over := ""
		if limit > 0 && len(body) > limit {
			over = "over"
		}
		if a.viewOverLimit.changed(id, over) && over != "" {
			a.logger.Warn("knob view over its caps view_bytes", "device_id", id, "bytes", len(body), "view_bytes", limit)
		}
	}
	h := fnv.New64a()
	_, _ = h.Write(body)
	demoID := ""
	if v.Demo != nil {
		demoID = v.Demo.ID
	}
	return body, fmt.Sprintf(`"%016x"`, h.Sum64()), demoID, nil
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
		func() {
			if v.Weather.val != nil {
				v.Weather.val.knobWeatherDetail = nil
			}
		},
		func() { v.NowPlaying = nil },
		func() { v.Usage = nil },
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

func (a *App) knobNowPlaying(now time.Time, accent bool) *knobNowPlaying {
	e, ok := a.nowPlaying.reg.Current(now)
	if !ok {
		return &knobNowPlaying{State: nowPlayingNone}
	}
	np := &knobNowPlaying{
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
	if img := e.Picture(nowplaying.Cover); accent && img != nil {
		np.Accent = img.Accent
		np.CoverVersion = e.CoverVersion()
		np.CoverLight = img.CoverLight
	}
	return np
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

func knobModel(m string) string {
	if m == defaultOpenMeteoModel {
		return ""
	}
	return m
}

func (a *App) knobWeather(now time.Time) *knobWeather { return a.knobWeatherFor(now, false) }

func (a *App) knobWeatherFor(now time.Time, detail bool) *knobWeather {
	cfg := a.cfg.Load().Weather
	wx := a.weather.state(cfg, now)
	if !wx.HaveObs || !wx.Enabled {
		return nil
	}
	w := &knobWeather{
		Provider: cfg.Provider,
		Model:    knobModel(cfg.model()),
		Cond:     wx.Obs.Condition,
		Code:     wx.Obs.ConditionCode,
		TempC:    wx.Obs.TempC,
		Stale:    !wx.Fresh,
		Severe:   wx.Obs.Severe,
		Night:    wx.Night,
	}
	if rise, set, ok := wx.roundedSun(); ok {
		r, s := rise.Unix(), set.Unix()
		w.Sunrise, w.Sunset = &r, &s
	}
	if !wx.Obs.ObservedAt.IsZero() {
		o := wx.Obs.ObservedAt.Unix()
		w.ObservedAt = &o
	}
	if detail {
		w.knobWeatherDetail = newKnobWeatherDetail(wx, cfg)
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
	a.noteDemoReport(id, r)
	w.Header().Set(knobViewWaitHeader, strconv.Itoa(int(knobViewWaitMax/time.Second)))
	if a.serveKnobViewWait(w, r, id, wait) {
		return
	}
	now := a.viewNow()
	body, etag, demoID, err := a.buildKnobView(id, now)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.deliverKnobView(w, r, id, demoID, body, etag, now)
}

func (a *App) deliverKnobView(w http.ResponseWriter, r *http.Request, id, demoID string, body []byte, etag string, now time.Time) {
	a.demos.setServed(id, demoID, true)
	if !a.writeKnobView(w, r, body, etag, now) {
		a.demos.setServed(id, demoID, false)
	}
}

func (a *App) writeKnobView(w http.ResponseWriter, r *http.Request, body []byte, etag string, now time.Time) bool {
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-cache")
	h.Set(knobNowHeader, strconv.FormatInt(now.Unix(), 10))
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(body)
	return err == nil
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
