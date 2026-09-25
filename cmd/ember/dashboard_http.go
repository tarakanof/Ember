package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// This file serves the read-only endpoints behind the native macOS dashboard
// (issue #110): usage, agent activity, weather and clock health. They are
// unauthenticated like /state and the previews, so none of them carries a
// secret — in particular no clock/ICS URL, Wi-Fi SSID/IP or home coordinates.
//
// Wire conventions, chosen for Swift's JSONDecoder (.iso8601) and Swift Charts:
// timestamps are RFC 3339 with whole seconds (the .iso8601 strategy rejects
// fractional seconds); "no value" is null rather than a zero sentinel; series
// are arrays of points; every quantity names its unit in the key (_sec,
// _percent, _c, _dbm, _bytes, _ugm3).

// wireTime truncates t to whole seconds so it marshals without a fraction.
func wireTime(t time.Time) time.Time { return t.Truncate(time.Second) }

// wireTimePtr is wireTime for optional instants: the zero time becomes nil.
func wireTimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	w := wireTime(t)
	return &w
}

// ---- GET /v1/usage ----

// usageWindowOut is one quota window on the wire.
type usageWindowOut struct {
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
	ResetLabel  string     `json:"reset_label,omitempty"`
}

// usageModelOut is a per-model window; models are sorted by name.
type usageModelOut struct {
	Model string `json:"model"`
	usageWindowOut
}

// usageToolOut is the latest snapshot one tool's producer posted.
type usageToolOut struct {
	Tool      string          `json:"tool"`
	Source    string          `json:"source,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
	Stale     bool            `json:"stale"` // older than stale_after_sec; the clock hides it too
	FiveHour  *usageWindowOut `json:"five_hour"`
	SevenDay  *usageWindowOut `json:"seven_day"`
	Models    []usageModelOut `json:"models"`
}

// usageSnapshotOut is the GET /v1/usage response.
type usageSnapshotOut struct {
	GeneratedAt   time.Time      `json:"generated_at"`
	StaleAfterSec int            `json:"stale_after_sec"`
	Tools         []usageToolOut `json:"tools"` // sorted by tool name
}

func usageWindowWire(w *UsageWindow) *usageWindowOut {
	if w == nil {
		return nil
	}
	out := &usageWindowOut{UsedPercent: w.UsedPercent, ResetLabel: w.ResetLabel}
	if w.ResetsAt > 0 {
		t := time.Unix(w.ResetsAt, 0)
		out.ResetsAt = &t
	}
	return out
}

// handleUsageSnapshot serves GET /v1/usage: the latest per-tool subscription
// usage the producers POSTed, which /state only leaks as the 5h percent.
func (a *App) handleUsageSnapshot(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	all := a.usage.All()
	out := usageSnapshotOut{
		GeneratedAt:   wireTime(now),
		StaleAfterSec: int(usageStaleTTL / time.Second),
		Tools:         make([]usageToolOut, 0, len(all)),
	}
	for tool, u := range all {
		t := usageToolOut{
			Tool:      tool,
			Source:    u.Source,
			UpdatedAt: wireTime(u.UpdatedAt),
			Stale:     now.Sub(u.UpdatedAt) > usageStaleTTL,
			FiveHour:  usageWindowWire(u.FiveHour),
			SevenDay:  usageWindowWire(u.SevenDay),
			Models:    make([]usageModelOut, 0, len(u.Models)),
		}
		for name, win := range u.Models {
			if mw := usageWindowWire(win); mw != nil {
				t.Models = append(t.Models, usageModelOut{Model: name, usageWindowOut: *mw})
			}
		}
		slices.SortFunc(t.Models, func(x, y usageModelOut) int { return cmp.Compare(x.Model, y.Model) })
		out.Tools = append(out.Tools, t)
	}
	slices.SortFunc(out.Tools, func(x, y usageToolOut) int { return cmp.Compare(x.Tool, y.Tool) })
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /v1/activity/summary ----

var errActivityUnavailable = errors.New("activity store is not available")

// activityTotalsOut is one rollup row; Key is the tool or source name and is
// omitted on the window total.
type activityTotalsOut struct {
	Key       string `json:"key,omitempty"`
	ActiveSec int    `json:"active_sec"`
	Sessions  int    `json:"sessions"`
	Attention int    `json:"attention"` // waiting episodes (agent asked for input)
}

// activityWindowOut summarises the heartbeats in [from, to).
type activityWindowOut struct {
	From     time.Time           `json:"from"`
	To       time.Time           `json:"to"`
	Total    activityTotalsOut   `json:"total"`
	ByTool   []activityTotalsOut `json:"by_tool"`   // most active first
	BySource []activityTotalsOut `json:"by_source"` // most active first
}

// activityDailyPoint is one (day, tool) bar for a stacked daily chart.
type activityDailyPoint struct {
	Day       string    `json:"day"`  // logical day, "2006-01-02"
	Date      time.Time `json:"date"` // local midnight of Day, for a chart's date axis
	Tool      string    `json:"tool"`
	ActiveSec int       `json:"active_sec"`
}

// activitySummaryOut is the GET /v1/activity/summary response.
type activitySummaryOut struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Recording is false while work_hours_include_activity is off: no new
	// heartbeats are stored, so recent windows read as zero.
	Recording  bool                 `json:"recording"`
	Days       int                  `json:"days"`
	SpanGapSec int                  `json:"span_gap_sec"`
	Today      activityWindowOut    `json:"today"`
	Period     activityWindowOut    `json:"period"` // the last Days logical days, today included
	Daily      []activityDailyPoint `json:"daily"`  // oldest first, zero-filled per tool
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
func activityGroups(m map[string]pomodoro.ActivityTotals) []activityTotalsOut {
	out := make([]activityTotalsOut, 0, len(m))
	for k, t := range m {
		out = append(out, activityTotalsOut{Key: k, ActiveSec: t.ActiveSec, Sessions: t.Sessions, Attention: t.Attention})
	}
	slices.SortFunc(out, func(x, y activityTotalsOut) int {
		return cmp.Or(cmp.Compare(y.ActiveSec, x.ActiveSec), cmp.Compare(x.Key, y.Key))
	})
	return out
}

func activityWindow(acts []pomodoro.ActivityRecord, from, to time.Time, dayStartHour int, loc *time.Location) activityWindowOut {
	var in []pomodoro.ActivityRecord
	for _, r := range acts {
		if !r.At.Before(from) {
			in = append(in, r)
		}
	}
	total := pomodoro.SummarizeActivity(in, activityAll, activitySpanGap, dayStartHour, loc)[""]
	return activityWindowOut{
		From:     wireTime(from),
		To:       wireTime(to),
		Total:    activityTotalsOut{ActiveSec: total.ActiveSec, Sessions: total.Sessions, Attention: total.Attention},
		ByTool:   activityGroups(pomodoro.SummarizeActivity(in, activityByTool, activitySpanGap, dayStartHour, loc)),
		BySource: activityGroups(pomodoro.SummarizeActivity(in, activityBySource, activitySpanGap, dayStartHour, loc)),
	}
}

// handleActivitySummary serves GET /v1/activity/summary?days=7 (1..90): agent
// activity (from the heartbeats behind the work-hours overlay) per tool and
// per source for today and the last `days` days, plus a daily per-tool series.
func (a *App) handleActivitySummary(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errActivityUnavailable)
		return
	}
	days := clampQueryInt(r, "days", 7, 1, 90)
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	now := time.Now()
	todayStart := logicalDayStart(now, p.DayStartHour, loc)
	periodStart := todayStart.AddDate(0, 0, -(days - 1))
	acts, err := a.store.ActivityBetween(periodStart, now.Add(time.Minute))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	period := activityWindow(acts, periodStart, now, p.DayStartHour, loc)
	tools := make([]string, 0, len(period.ByTool))
	for _, g := range period.ByTool {
		tools = append(tools, g.Key)
	}
	slices.Sort(tools)
	perDay := pomodoro.DailyActiveSec(acts, activityByTool, activitySpanGap, p.DayStartHour, loc)
	daily := make([]activityDailyPoint, 0, days*len(tools))
	for i := days - 1; i >= 0; i-- {
		start := todayStart.AddDate(0, 0, -i)
		key := start.Format("2006-01-02")
		midnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
		for _, tool := range tools {
			daily = append(daily, activityDailyPoint{Day: key, Date: midnight, Tool: tool, ActiveSec: perDay[key][tool]})
		}
	}

	writeJSON(w, http.StatusOK, activitySummaryOut{
		GeneratedAt: wireTime(now),
		Recording:   p.WorkHoursIncludeActivity,
		Days:        days,
		SpanGapSec:  int(activitySpanGap / time.Second),
		Today:       activityWindow(acts, todayStart, now, p.DayStartHour, loc),
		Period:      period,
		Daily:       daily,
	})
}

// ---- GET /v1/weather/state ----

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
	FetchedAt time.Time   `json:"fetched_at"`
	Stale     bool        `json:"stale"` // older than the tile TTL; the clock has dropped the tile
	Condition string      `json:"condition"`
	Severe    bool        `json:"severe"`
	TempC     float64     `json:"temp_c"`
	Hourly    []tempPoint `json:"hourly"`
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

// sunOut is today's sunrise and sunset at the configured location.
type sunOut struct {
	Sunrise time.Time `json:"sunrise"`
	Sunset  time.Time `json:"sunset"`
}

// weatherStateOut is the GET /v1/weather/state response. Temperatures are
// always Celsius; Units is the user's display preference for converting.
type weatherStateOut struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Enabled     bool               `json:"enabled"`
	Provider    string             `json:"provider"`
	Units       string             `json:"units"`
	Current     *weatherCurrentOut `json:"current"` // null until the first successful fetch
	Air         *airOut            `json:"air"`     // null until the first air-quality fetch
	Sun         *sunOut            `json:"sun"`     // null without a location, or in polar day/night
}

// hourlyBase is the hour the provider's hourly series starts at: both
// providers begin at the current hour of the fetch.
func hourlyBase(fetched time.Time) time.Time { return fetched.Truncate(time.Hour) }

// handleWeatherState serves GET /v1/weather/state: the poller's cached
// observation (no provider call). The location itself is never echoed.
func (a *App) handleWeatherState(w http.ResponseWriter, r *http.Request) {
	cfg := a.cfg.Load().Weather
	now := time.Now()
	out := weatherStateOut{GeneratedAt: wireTime(now), Enabled: cfg.Enabled, Provider: cfg.Provider, Units: cfg.Units}

	if obs, ok := a.weather.current(); ok {
		cur := &weatherCurrentOut{
			FetchedAt: wireTime(obs.FetchedAt),
			Stale:     now.Sub(obs.FetchedAt) >= weatherTileStaleTTL,
			Condition: obs.Condition,
			Severe:    obs.Severe,
			TempC:     obs.TempC,
			Hourly:    make([]tempPoint, 0, len(obs.Hourly)),
		}
		base := hourlyBase(obs.FetchedAt)
		for i, v := range obs.Hourly {
			cur.Hourly = append(cur.Hourly, tempPoint{Time: base.Add(time.Duration(i) * time.Hour), TempC: v})
		}
		out.Current = cur
	}
	if air, ok := a.weather.currentAir(); ok {
		ao := &airOut{
			FetchedAt: wireTime(air.FetchedAt),
			Stale:     now.Sub(air.FetchedAt) >= weatherTileStaleTTL,
			AQI:       air.AQI,
			PM25:      air.PM25,
			PM10:      air.PM10,
			Hourly:    make([]aqiPoint, 0, len(air.HourlyAQI)),
		}
		base := hourlyBase(air.FetchedAt)
		for i, v := range air.HourlyAQI {
			ao.Hourly = append(ao.Hourly, aqiPoint{Time: base.Add(time.Duration(i) * time.Hour), AQI: v})
		}
		out.Air = ao
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		if rise, set, ok := sunTimes(cfg.Latitude, cfg.Longitude, now); ok {
			out.Sun = &sunOut{Sunrise: wireTime(rise), Sunset: wireTime(set)}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /v1/clock/health ----

// clockProbeTTL bounds how often the unauthenticated health endpoint may reach
// the clock: however many viewers poll it, the clock sees one GET per window.
const clockProbeTTL = 30 * time.Second

// clockProbeTimeout bounds one health probe. The clock's Wi-Fi is lossy; a
// dashboard would rather show "unreachable" than hang.
const clockProbeTimeout = 3 * time.Second

// clockProbeCache holds the last GET /api/v1/device result. The zero value is
// ready to use.
type clockProbeCache struct {
	mu   sync.Mutex // protects all fields; held across a probe to single-flight it
	at   time.Time
	base string // the clock URL probed; a different URL invalidates the entry
	dev  clockDeviceOut
}

// clockDeviceOut is the clock's own telemetry. Pointers are null when the
// clock is unreachable or its firmware doesn't report the field.
type clockDeviceOut struct {
	Reachable        bool      `json:"reachable"`
	CheckedAt        time.Time `json:"checked_at"`
	Firmware         string    `json:"firmware,omitempty"`
	UptimeSec        *int64    `json:"uptime_sec"`
	FreeHeapBytes    *int64    `json:"free_heap_bytes"`
	MinFreeHeapBytes *int64    `json:"min_free_heap_bytes"`
	WifiRSSIDbm      *int      `json:"wifi_rssi_dbm"`
	WifiConnects     *int      `json:"wifi_connects"` // (re)connects since boot; >1 means the link dropped
	ResetReason      string    `json:"reset_reason,omitempty"`
	FPS              *float64  `json:"fps"`
	BatteryPercent   *float64  `json:"battery_percent"`
	TemperatureC     *float64  `json:"temperature_c"`
	HumidityPercent  *float64  `json:"humidity_percent"`
}

// publishHealthOut is the server→clock push record since the server started.
type publishHealthOut struct {
	CountingSince time.Time  `json:"counting_since"` // server start; the counters reset on restart
	OKTotal       int64      `json:"ok_total"`
	FailTotal     int64      `json:"fail_total"`
	RetriesTotal  int64      `json:"retries_total"` // lost first attempts that a retry recovered
	SuccessRatio  *float64   `json:"success_ratio"` // ok/(ok+fail), 0..1; null before the first publish
	LastAt        *time.Time `json:"last_at"`
	LastOK        bool       `json:"last_ok"`
}

// clockHealthOut is the GET /v1/clock/health response.
type clockHealthOut struct {
	GeneratedAt  time.Time        `json:"generated_at"`
	Publish      publishHealthOut `json:"publish"`
	Device       *clockDeviceOut  `json:"device"` // null when no clock is configured
	LastButtonAt *time.Time       `json:"last_button_at"`
}

// clockDeviceWire decodes the subset of awtrix-ng's GET /api/v1/device the
// health view needs. Everything else in that payload (IP, SSID host, UID) is
// deliberately dropped: this endpoint is unauthenticated.
type clockDeviceWire struct {
	Version     string   `json:"version"`
	WifiRSSI    *int     `json:"wifiRssi"`
	Uptime      *int64   `json:"uptimeSeconds"`
	FreeHeap    *int64   `json:"freeHeapBytes"`
	MinFreeHeap *int64   `json:"minFreeHeapBytes"`
	ResetReason string   `json:"resetReason"`
	FPS         *float64 `json:"fps"`
	Battery     *float64 `json:"batteryPercent"`
	Temperature *float64 `json:"temperature"`
	Humidity    *float64 `json:"humidity"`
	WiFi        struct {
		Connects *int `json:"connects"`
	} `json:"wifi"`
}

// probeClockHealth returns the cached device telemetry, refreshing it from the
// clock when older than clockProbeTTL. nil means no clock is configured.
func (a *App) probeClockHealth(ctx context.Context, now time.Time) *clockDeviceOut {
	base := a.cfg.Load().AWTRIX.HTTPBaseURL
	if base == "" {
		return nil
	}
	c := &a.clockProbe
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base == base && !c.at.IsZero() && now.Sub(c.at) < clockProbeTTL {
		dev := c.dev
		return &dev
	}

	pctx, cancel := context.WithTimeout(ctx, clockProbeTimeout)
	defer cancel()
	dev := clockDeviceOut{CheckedAt: wireTime(now)}
	body, status, err := a.proxyToDevice(pctx, http.MethodGet, "/api/v1/device", nil)
	if err == nil && status == http.StatusOK {
		dev.Reachable = true
		var raw clockDeviceWire
		if json.Unmarshal(body, &raw) == nil {
			dev.Firmware = raw.Version
			dev.UptimeSec = raw.Uptime
			dev.FreeHeapBytes = raw.FreeHeap
			dev.MinFreeHeapBytes = raw.MinFreeHeap
			dev.WifiRSSIDbm = raw.WifiRSSI
			dev.WifiConnects = raw.WiFi.Connects
			dev.ResetReason = raw.ResetReason
			dev.FPS = raw.FPS
			dev.BatteryPercent = raw.Battery
			dev.TemperatureC = raw.Temperature
			dev.HumidityPercent = raw.Humidity
		}
	}
	c.at, c.base, c.dev = now, base, dev
	return &dev
}

// handleClockHealth serves GET /v1/clock/health: publish success, the last
// publish, and the clock's Wi-Fi/heap/uptime telemetry, without scraping
// /metrics or needing the token that /v1/device/stats sits behind.
func (a *App) handleClockHealth(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	a.mu.Lock()
	lastAt, lastOK := a.lastPublishAt, a.lastPublishOK
	a.mu.Unlock()

	pub := publishHealthOut{
		CountingSince: wireTime(a.startedAt),
		OKTotal:       a.metrics.publishTotalOK.Load(),
		FailTotal:     a.metrics.publishTotalFail.Load(),
		RetriesTotal:  a.metrics.publishRetries.Load(),
		LastAt:        wireTimePtr(lastAt),
		LastOK:        lastOK,
	}
	if n := pub.OKTotal + pub.FailTotal; n > 0 {
		ratio := float64(pub.OKTotal) / float64(n)
		pub.SuccessRatio = &ratio
	}
	var lastButton *time.Time
	if s := a.lastButtonAt.Load(); s > 0 {
		t := time.Unix(s, 0)
		lastButton = &t
	}
	writeJSON(w, http.StatusOK, clockHealthOut{
		GeneratedAt:  wireTime(now),
		Publish:      pub,
		Device:       a.probeClockHealth(r.Context(), now),
		LastButtonAt: lastButton,
	})
}
