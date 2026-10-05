package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
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
// weather null when disabled or never fetched. weather.night is the sun
// schedule's call; sunrise/sunset are the location's local day, informational.
// nowplaying appears only when the device's pages turn "nowplaying" on, so
// a knob without that page gets the same bytes and ETag as before.
type knobView struct {
	V             int             `json:"v"`
	Epoch         uint64          `json:"epoch"`
	ConfigVersion int             `json:"config_version"`
	Mood          knobMood        `json:"mood"`
	Pomo          *knobPomo       `json:"pomo"`
	Weather       *knobWeather    `json:"weather"`
	Brightness    knobLight       `json:"brightness"`
	NowPlaying    *knobNowPlaying `json:"nowplaying,omitempty"`
	DiagLiveUntil *int64          `json:"diag_live_until,omitempty"`
}

// knobMood: the /state render counters and source, then who leads the
// winning state (cinder#42, knobLeadOf). lead is left out when it equals
// source but for case (one host), hosts when it is 0 or 1; all four are omitted for an
// idle view, so its bytes and ETag are as before.
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

func newKnobMood(r Render, l knobLead) knobMood {
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

// knobNowPlaying is the view's music block. state none carries nothing
// else. position_ms is the position at position_at (server Unix ms), so the
// block moves only on a real change and the knob extrapolates; art_version
// changes when the pictures do (fetch /v1/nowplaying/art then).
type knobNowPlaying struct {
	State      string `json:"state"`
	Source     string `json:"source,omitempty"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
	PositionAt int64  `json:"position_at,omitempty"`
	ArtVersion string `json:"art_version,omitempty"`
	AlbumArt   bool   `json:"album_art,omitempty"`
	ArtistArt  bool   `json:"artist_art,omitempty"`
}

// knobNowPlayingPage is the knob page id that opts a device into the block.
const knobNowPlayingPage = "nowplaying"

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
	cfg, _, err := a.devices.config(id)
	if err != nil {
		return nil, "", err
	}
	sv := a.sessions.View()
	r := a.legacyRender(sv)
	b := a.currentBrightness(now)
	v := knobView{
		V:             1,
		Epoch:         epoch,
		ConfigVersion: version,
		Mood:          newKnobMood(r, knobLeadOf(sv)),
		Pomo:          a.knobPomo(now),
		Weather:       a.knobWeather(now),
		Brightness:    knobLight{Level: b.Level, Night: b.Night},
		DiagLiveUntil: a.knobLiveUnix(id, cfg.Diagnostics, now),
	}
	if cfg.pageOn(knobNowPlayingPage) {
		v.NowPlaying = a.knobNowPlaying(now)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, "", fmt.Errorf("encode knob view: %w", err)
	}
	h := fnv.New64a()
	_, _ = h.Write(body)
	return body, fmt.Sprintf(`"%016x"`, h.Sum64()), nil
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
		DurationMS: e.DurationMS,
		PositionMS: e.PositionMS,
		PositionAt: e.PositionAt.UnixMilli(),
		ArtVersion: e.ArtVersion(),
		AlbumArt:   e.AlbumArt != nil,
		ArtistArt:  e.ArtistArt != nil,
	}
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
		_, w.Night = sunLevel(a.cfg.Load().Brightness.resolved(), cfg.Latitude, cfg.Longitude, now)
		if rise, set, ok := sunTimes(cfg.Latitude, cfg.Longitude, localNoon(now, obs, cfg.Longitude)); ok {
			r, s := rise.Round(sunRounding).Unix(), set.Round(sunRounding).Unix()
			w.Sunrise, w.Sunset = &r, &s
		}
	}
	return w
}

// localNoon is noon on the location's own date at now, as an instant, so
// sunTimes (which works per UTC date) yields that local day's events. The
// offset is the observation's, else the longitude's hour.
func localNoon(now time.Time, obs weatherObservation, lon float64) time.Time {
	off := time.Duration(math.Round(lon/15)) * time.Hour
	if obs.TZKnown {
		off = time.Duration(obs.TZOffsetSeconds) * time.Second
	}
	y, m, d := now.UTC().Add(off).Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Add(-off)
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
	now := time.Now()
	body, etag, err := a.knobView(id, now)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.writeKnobView(w, r, body, etag, now)
}

// writeKnobView answers body, or 304 when the request's If-None-Match
// matches etag. X-Ember-Now is now: the moment the answer leaves, so a
// long-poll's 304 carries the time at its end, not its start.
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
