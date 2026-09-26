package main

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// This file serves the read-only endpoints behind the native macOS dashboard
// (issue #110): usage, agent activity and weather; clock health lives in
// clock_health_http.go. They are unauthenticated like /state and the previews,
// so none of them carries a secret: no clock/ICS URL, Wi-Fi SSID/IP, device
// UID, button presses, or home coordinates (sun times are rounded for that).
//
// Wire conventions, chosen for Swift's JSONDecoder (.iso8601) and Swift Charts:
// timestamps are RFC 3339 with whole seconds (the .iso8601 strategy rejects
// fractional seconds); "no value" is null rather than a zero sentinel; series
// are arrays of points; every quantity names its unit in the key (_sec,
// _percent, _c, _dbm, _bytes, _ugm3).
//
// Each handler is a thin wrapper over a build* method that takes `now`; the
// response is rendered in now's location. The golden tests
// (testdata/dashboard/*.json) call the builders with a fixed instant, and the
// EmberKit decode tests read the same files.

// errDashboardInternal is the body of a 500 on an open endpoint; the cause is
// logged, not returned, so storage errors don't leak to unauthenticated callers.
var errDashboardInternal = errors.New("internal error")

// wireTime renders t in loc, truncated to whole seconds so it marshals without
// a fraction.
func wireTime(t time.Time, loc *time.Location) time.Time { return t.In(loc).Truncate(time.Second) }

// wireTimePtr is wireTime for optional instants: the zero time becomes nil.
func wireTimePtr(t time.Time, loc *time.Location) *time.Time {
	if t.IsZero() {
		return nil
	}
	w := wireTime(t, loc)
	return &w
}

// ---- GET /v1/usage ----

// usageWindowOut is one quota window on the wire.
type usageWindowOut struct {
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
	ResetLabel  *string    `json:"reset_label"`
}

// usageToolOut is the latest snapshot one tool's producer posted.
type usageToolOut struct {
	Tool      string                    `json:"tool"`
	Source    *string                   `json:"source"`
	UpdatedAt time.Time                 `json:"updated_at"`
	Stale     bool                      `json:"stale"` // older than stale_after_sec; the clock hides it too
	FiveHour  *usageWindowOut           `json:"five_hour"`
	SevenDay  *usageWindowOut           `json:"seven_day"`
	Models    map[string]usageWindowOut `json:"models"` // keyed by model name, e.g. "opus"
}

// usageSnapshotOut is the GET /v1/usage response.
type usageSnapshotOut struct {
	GeneratedAt   time.Time      `json:"generated_at"`
	StaleAfterSec int            `json:"stale_after_sec"`
	Tools         []usageToolOut `json:"tools"` // sorted by tool name
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func usageWindowWire(w *UsageWindow, loc *time.Location) *usageWindowOut {
	if w == nil {
		return nil
	}
	out := &usageWindowOut{UsedPercent: w.UsedPercent, ResetLabel: optString(w.ResetLabel)}
	if w.ResetsAt > 0 {
		t := time.Unix(w.ResetsAt, 0).In(loc)
		out.ResetsAt = &t
	}
	return out
}

// buildUsageSnapshot assembles GET /v1/usage as of now.
func (a *App) buildUsageSnapshot(now time.Time) usageSnapshotOut {
	loc := now.Location()
	all := a.usage.All()
	out := usageSnapshotOut{
		GeneratedAt:   wireTime(now, loc),
		StaleAfterSec: int(usageStaleTTL / time.Second),
		Tools:         make([]usageToolOut, 0, len(all)),
	}
	for tool, u := range all {
		t := usageToolOut{
			Tool:      tool,
			Source:    optString(u.Source),
			UpdatedAt: wireTime(u.UpdatedAt, loc),
			Stale:     now.Sub(u.UpdatedAt) > usageStaleTTL,
			FiveHour:  usageWindowWire(u.FiveHour, loc),
			SevenDay:  usageWindowWire(u.SevenDay, loc),
			Models:    make(map[string]usageWindowOut, len(u.Models)),
		}
		for name, win := range u.Models {
			if mw := usageWindowWire(win, loc); mw != nil {
				t.Models[name] = *mw
			}
		}
		out.Tools = append(out.Tools, t)
	}
	slices.SortFunc(out.Tools, func(x, y usageToolOut) int { return cmp.Compare(x.Tool, y.Tool) })
	return out
}

// handleUsageSnapshot serves GET /v1/usage: the latest per-tool subscription
// usage the producers POSTed, which /state only leaks as the 5h percent.
func (a *App) handleUsageSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildUsageSnapshot(time.Now()))
}

// ---- GET /v1/activity/summary ----

var errActivityUnavailable = errors.New("activity store is not available")

// activityMaxDays caps ?days: the endpoint is open and each request walks every
// heartbeat in the window on the store's single connection.
const activityMaxDays = 90

// sourceColorMemo remembers the last source_color each producer source posted,
// so activity charts can colour sources that have no live session. In memory
// only: it refills as producers post. The zero value is ready to use.
type sourceColorMemo struct {
	mu     sync.Mutex // protects colors
	colors map[string]string
}

func (m *sourceColorMemo) remember(source string, color *string) {
	if color == nil || *color == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.colors == nil {
		m.colors = map[string]string{}
	}
	m.colors[source] = *color
}

// lookup returns the remembered colour for source, or nil.
func (m *sourceColorMemo) lookup(source string) *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.colors[source]; ok {
		return &c
	}
	return nil
}

// activityTotalsOut is one rollup row. Key is the tool or source name and is
// omitted on the window total; SourceColor is set on by_source rows when known.
type activityTotalsOut struct {
	Key         string  `json:"key,omitempty"`
	SourceColor *string `json:"source_color,omitempty"`
	ActiveSec   int     `json:"active_sec"`
	Sessions    int     `json:"sessions"`
	Attention   int     `json:"attention"` // waiting episodes (agent asked for input)
}

// activityWindowOut summarises the heartbeats in [from, to).
type activityWindowOut struct {
	From     time.Time           `json:"from"`
	To       time.Time           `json:"to"`
	Total    activityTotalsOut   `json:"total"`
	ByTool   []activityTotalsOut `json:"by_tool"`   // most active first
	BySource []activityTotalsOut `json:"by_source"` // most active first
}

// activityToolDay is one (day, tool) bar for a stacked daily chart.
type activityToolDay struct {
	Day       string    `json:"day"`  // logical day, "2006-01-02"
	Date      time.Time `json:"date"` // local midnight of Day, for a chart's date axis
	Tool      string    `json:"tool"`
	ActiveSec int       `json:"active_sec"`
	Sessions  int       `json:"sessions"`
	Attention int       `json:"attention"`
}

// activitySourceDay is one (day, source) bar, coloured by the source.
type activitySourceDay struct {
	Day         string    `json:"day"`
	Date        time.Time `json:"date"`
	Source      string    `json:"source"`
	SourceColor *string   `json:"source_color"` // "#RRGGBB"; null until the source posts one
	ActiveSec   int       `json:"active_sec"`
	Sessions    int       `json:"sessions"`
	Attention   int       `json:"attention"`
}

// activitySummaryOut is the GET /v1/activity/summary response.
type activitySummaryOut struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Recording is false while work_hours_include_activity is off: no new
	// heartbeats are stored, so recent windows read as zero.
	Recording bool `json:"recording"`
	Days      int  `json:"days"`
	// SpanGapSec: running heartbeats of one session no more than this apart
	// form one active span. A span ends at its last heartbeat and a lone
	// heartbeat is zero-width, so short bursts under-count by up to one
	// recording interval (2 min); spans are also cut at the day start.
	SpanGapSec    int                 `json:"span_gap_sec"`
	Today         activityWindowOut   `json:"today"`
	Period        activityWindowOut   `json:"period"`          // the last Days logical days, today included
	Daily         []activityToolDay   `json:"daily"`           // oldest first, zero-filled per tool
	DailyBySource []activitySourceDay `json:"daily_by_source"` // oldest first, zero-filled per source
}

// logicalDayStart is the instant the logical day containing t began: the
// day-start hour on that calendar day in loc (see logicalDayKey).
func logicalDayStart(t time.Time, dayStartHour int, loc *time.Location) time.Time {
	d := t.In(loc).Add(-time.Duration(dayStartHour) * time.Hour)
	return time.Date(d.Year(), d.Month(), d.Day(), dayStartHour, 0, 0, 0, loc)
}

func activityByTool(r pomodoro.ActivityRecord) string   { return r.Tool }
func activityBySource(r pomodoro.ActivityRecord) string { return r.Source }
func activityAll(pomodoro.ActivityRecord) string        { return "" }

// activityGroups flattens a rollup into rows, most active first.
func activityGroups(m map[string]pomodoro.ActivityTotals, color func(string) *string) []activityTotalsOut {
	out := make([]activityTotalsOut, 0, len(m))
	for k, t := range m {
		row := activityTotalsOut{Key: k, ActiveSec: t.ActiveSec, Sessions: t.Sessions, Attention: t.Attention}
		if color != nil {
			row.SourceColor = color(k)
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y activityTotalsOut) int {
		return cmp.Or(cmp.Compare(y.ActiveSec, x.ActiveSec), cmp.Compare(x.Key, y.Key))
	})
	return out
}

func (a *App) activityWindow(acts []pomodoro.ActivityRecord, from, to time.Time, dayStartHour int) activityWindowOut {
	loc := to.Location()
	var in []pomodoro.ActivityRecord
	for _, r := range acts {
		if !r.At.Before(from) {
			in = append(in, r)
		}
	}
	total := pomodoro.SummarizeActivity(in, activityAll, activitySpanGap, dayStartHour, loc)[""]
	return activityWindowOut{
		From:     wireTime(from, loc),
		To:       wireTime(to, loc),
		Total:    activityTotalsOut{ActiveSec: total.ActiveSec, Sessions: total.Sessions, Attention: total.Attention},
		ByTool:   activityGroups(pomodoro.SummarizeActivity(in, activityByTool, activitySpanGap, dayStartHour, loc), nil),
		BySource: activityGroups(pomodoro.SummarizeActivity(in, activityBySource, activitySpanGap, dayStartHour, loc), a.sourceColors.lookup),
	}
}

// buildActivitySummary assembles GET /v1/activity/summary for the last `days`
// logical days as of now; days and hours bucket in now's location.
func (a *App) buildActivitySummary(now time.Time, days int) (activitySummaryOut, error) {
	p := a.cfg.Load().Pomodoro
	loc := now.Location()
	todayStart := logicalDayStart(now, p.DayStartHour, loc)
	periodStart := todayStart.AddDate(0, 0, -(days - 1))
	acts, err := a.store.ActivityBetween(periodStart, now.Add(time.Minute))
	if err != nil {
		return activitySummaryOut{}, err
	}

	period := a.activityWindow(acts, periodStart, now, p.DayStartHour)
	keys := func(rows []activityTotalsOut) []string {
		out := make([]string, 0, len(rows))
		for _, g := range rows {
			out = append(out, g.Key)
		}
		slices.Sort(out)
		return out
	}
	tools, sources := keys(period.ByTool), keys(period.BySource)
	byTool := pomodoro.DailyActivity(acts, activityByTool, activitySpanGap, p.DayStartHour, loc)
	bySource := pomodoro.DailyActivity(acts, activityBySource, activitySpanGap, p.DayStartHour, loc)

	daily := make([]activityToolDay, 0, days*len(tools))
	dailyBySource := make([]activitySourceDay, 0, days*len(sources))
	for i := days - 1; i >= 0; i-- {
		start := todayStart.AddDate(0, 0, -i)
		key := start.Format("2006-01-02")
		midnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
		for _, tool := range tools {
			t := byTool[key][tool]
			daily = append(daily, activityToolDay{Day: key, Date: midnight, Tool: tool,
				ActiveSec: t.ActiveSec, Sessions: t.Sessions, Attention: t.Attention})
		}
		for _, src := range sources {
			t := bySource[key][src]
			dailyBySource = append(dailyBySource, activitySourceDay{Day: key, Date: midnight, Source: src,
				SourceColor: a.sourceColors.lookup(src), ActiveSec: t.ActiveSec, Sessions: t.Sessions, Attention: t.Attention})
		}
	}

	return activitySummaryOut{
		GeneratedAt:   wireTime(now, loc),
		Recording:     p.WorkHoursIncludeActivity,
		Days:          days,
		SpanGapSec:    int(activitySpanGap / time.Second),
		Today:         a.activityWindow(acts, todayStart, now, p.DayStartHour),
		Period:        period,
		Daily:         daily,
		DailyBySource: dailyBySource,
	}, nil
}

// handleActivitySummary serves GET /v1/activity/summary?days=7 (1..90): agent
// activity (from the heartbeats behind the work-hours overlay) per tool and per
// source for today and the last `days` days, plus daily per-tool and
// per-source series.
func (a *App) handleActivitySummary(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errActivityUnavailable)
		return
	}
	days := clampQueryInt(r, "days", 7, 1, activityMaxDays)
	out, err := a.buildActivitySummary(time.Now().In(a.statsLoc()), days)
	if err != nil {
		a.logger.ErrorContext(r.Context(), "activity summary failed", "err", err)
		writeError(w, http.StatusInternalServerError, errDashboardInternal)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /v1/weather/state ----

// sunRounding coarsens sunrise/sunset on the open endpoint: to the second they
// pin the configured coordinates to a few hundred metres, while the dashboard
// only shows HH:mm.
const sunRounding = 5 * time.Minute

// tempPoint is one hourly forecast temperature.
type tempPoint struct {
	Time  time.Time `json:"time"`
	TempC float64   `json:"temp_c"`
}

// aqiPoint is one hourly European AQI forecast value.
type aqiPoint struct {
	Time time.Time `json:"time"`
	AQI  float64   `json:"european_aqi"`
}

// weatherCurrentOut is the cached provider observation.
type weatherCurrentOut struct {
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"stale"` // older than the tile TTL; the clock has dropped the tile
	Condition string    `json:"condition"`
	// ConditionCode is the provider's raw code (WMO code for open-meteo,
	// symbol_code for met-no); Provider says which. Null on old observations.
	ConditionCode *string `json:"condition_code"`
	Severe        bool    `json:"severe"`
	TempC         float64 `json:"temp_c"`
	// Hourly is empty when the provider didn't stamp the series' start.
	Hourly []tempPoint `json:"hourly"`
}

// airOut is the cached air-quality observation.
type airOut struct {
	FetchedAt time.Time  `json:"fetched_at"`
	Stale     bool       `json:"stale"`
	AQI       float64    `json:"european_aqi"`
	PM25      float64    `json:"pm2_5_ugm3"`
	PM10      float64    `json:"pm10_ugm3"`
	Hourly    []aqiPoint `json:"hourly"`
}

// sunOut is today's sunrise and sunset, rounded to sunRounding.
type sunOut struct {
	Sunrise time.Time `json:"sunrise"`
	Sunset  time.Time `json:"sunset"`
}

// weatherStateOut is the GET /v1/weather/state response. Temperatures are
// always Celsius; Units is the user's display preference for converting.
type weatherStateOut struct {
	GeneratedAt time.Time `json:"generated_at"`
	Enabled     bool      `json:"enabled"`
	Provider    string    `json:"provider"`
	Units       string    `json:"units"`
	// LocationName is the label the user typed for the location (never the
	// coordinates); null when unset.
	LocationName *string            `json:"location_name"`
	Current      *weatherCurrentOut `json:"current"` // null until the first successful fetch
	Air          *airOut            `json:"air"`     // null until the first air-quality fetch
	Sun          *sunOut            `json:"sun"`     // null without a location, or in polar day/night
}

// buildWeatherState assembles GET /v1/weather/state from the poller's cache.
func (a *App) buildWeatherState(now time.Time) weatherStateOut {
	loc := now.Location()
	cfg := a.cfg.Load().Weather
	out := weatherStateOut{
		GeneratedAt:  wireTime(now, loc),
		Enabled:      cfg.Enabled,
		Provider:     cfg.Provider,
		Units:        cfg.Units,
		LocationName: optString(cfg.LocationName),
	}

	if obs, ok := a.weather.current(); ok {
		cur := &weatherCurrentOut{
			FetchedAt:     wireTime(obs.FetchedAt, loc),
			Stale:         now.Sub(obs.FetchedAt) >= weatherTileStaleTTL,
			Condition:     obs.Condition,
			ConditionCode: optString(obs.ConditionCode),
			Severe:        obs.Severe,
			TempC:         obs.TempC,
			Hourly:        []tempPoint{},
		}
		if !obs.HourlyStart.IsZero() {
			for i, v := range obs.Hourly {
				cur.Hourly = append(cur.Hourly, tempPoint{Time: wireTime(obs.HourlyStart.Add(time.Duration(i)*time.Hour), loc), TempC: v})
			}
		}
		out.Current = cur
	}
	if air, ok := a.weather.currentAir(); ok {
		ao := &airOut{
			FetchedAt: wireTime(air.FetchedAt, loc),
			Stale:     now.Sub(air.FetchedAt) >= weatherTileStaleTTL,
			AQI:       air.AQI,
			PM25:      air.PM25,
			PM10:      air.PM10,
			Hourly:    []aqiPoint{},
		}
		if !air.HourlyStart.IsZero() {
			for i, v := range air.HourlyAQI {
				ao.Hourly = append(ao.Hourly, aqiPoint{Time: wireTime(air.HourlyStart.Add(time.Duration(i)*time.Hour), loc), AQI: v})
			}
		}
		out.Air = ao
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		if rise, set, ok := sunTimes(cfg.Latitude, cfg.Longitude, now); ok {
			out.Sun = &sunOut{Sunrise: rise.Round(sunRounding).In(loc), Sunset: set.Round(sunRounding).In(loc)}
		}
	}
	return out
}

// handleWeatherState serves GET /v1/weather/state: the poller's cached
// observation (no provider call). The coordinates are never echoed.
func (a *App) handleWeatherState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildWeatherState(time.Now()))
}
