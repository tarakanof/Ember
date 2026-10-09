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

var errDashboardInternal = errors.New("internal error")

func wireTime(t time.Time, loc *time.Location) time.Time { return t.In(loc).Truncate(time.Second) }

func wireTimePtr(t time.Time, loc *time.Location) *time.Time {
	if t.IsZero() {
		return nil
	}
	w := wireTime(t, loc)
	return &w
}

type usageWindowOut struct {
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
	ResetLabel  *string    `json:"reset_label"`
}

type usageToolOut struct {
	Tool      string                    `json:"tool"`
	Source    *string                   `json:"source"`
	UpdatedAt time.Time                 `json:"updated_at"`
	Stale     bool                      `json:"stale"`
	FiveHour  *usageWindowOut           `json:"five_hour"`
	SevenDay  *usageWindowOut           `json:"seven_day"`
	Models    map[string]usageWindowOut `json:"models"`
}

type usageSnapshotOut struct {
	GeneratedAt   time.Time      `json:"generated_at"`
	StaleAfterSec int            `json:"stale_after_sec"`
	Tools         []usageToolOut `json:"tools"`
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

func (a *App) handleUsageSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildUsageSnapshot(time.Now()))
}

var errActivityUnavailable = errors.New("activity store is not available")

const activityMaxDays = 90

type sourceColorMemo struct {
	mu     sync.Mutex
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

func (m *sourceColorMemo) lookup(source string) *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.colors[source]; ok {
		return &c
	}
	return nil
}

type activityTotalsOut struct {
	Key       string `json:"key,omitempty"`
	ActiveSec int    `json:"active_sec"`
	Sessions  int    `json:"sessions"`
	Attention int    `json:"attention"`
}

type activitySourceTotalsOut struct {
	activityTotalsOut
	SourceColor *string `json:"source_color"`
}

type activityWindowOut struct {
	From     time.Time                 `json:"from"`
	To       time.Time                 `json:"to"`
	Total    activityTotalsOut         `json:"total"`
	ByTool   []activityTotalsOut       `json:"by_tool"`
	BySource []activitySourceTotalsOut `json:"by_source"`
}

type activityToolDay struct {
	Day       string    `json:"day"`
	Date      time.Time `json:"date"`
	Tool      string    `json:"tool"`
	ActiveSec int       `json:"active_sec"`
	Sessions  int       `json:"sessions"`
	Attention int       `json:"attention"`
}

type activitySourceDay struct {
	Day         string    `json:"day"`
	Date        time.Time `json:"date"`
	Source      string    `json:"source"`
	SourceColor *string   `json:"source_color"`
	ActiveSec   int       `json:"active_sec"`
	Sessions    int       `json:"sessions"`
	Attention   int       `json:"attention"`
}

type activitySummaryOut struct {
	GeneratedAt   time.Time           `json:"generated_at"`
	Recording     bool                `json:"recording"`
	Days          int                 `json:"days"`
	SpanGapSec    int                 `json:"span_gap_sec"`
	Today         activityWindowOut   `json:"today"`
	Period        activityWindowOut   `json:"period"`
	Daily         []activityToolDay   `json:"daily"`
	DailyBySource []activitySourceDay `json:"daily_by_source"`
}

func logicalDayStart(t time.Time, dayStartHour int, loc *time.Location) time.Time {
	d := t.In(loc).Add(-time.Duration(dayStartHour) * time.Hour)
	return time.Date(d.Year(), d.Month(), d.Day(), dayStartHour, 0, 0, 0, loc)
}

func activityByTool(r pomodoro.ActivityRecord) string   { return r.Tool }
func activityBySource(r pomodoro.ActivityRecord) string { return r.Source }
func activityAll(pomodoro.ActivityRecord) string        { return "" }

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

func (a *App) sourceGroups(m map[string]pomodoro.ActivityTotals) []activitySourceTotalsOut {
	rows := activityGroups(m)
	out := make([]activitySourceTotalsOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, activitySourceTotalsOut{activityTotalsOut: r, SourceColor: a.sourceColors.lookup(r.Key)})
	}
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
		ByTool:   activityGroups(pomodoro.SummarizeActivity(in, activityByTool, activitySpanGap, dayStartHour, loc)),
		BySource: a.sourceGroups(pomodoro.SummarizeActivity(in, activityBySource, activitySpanGap, dayStartHour, loc)),
	}
}

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
	tools := make([]string, 0, len(period.ByTool))
	for _, g := range period.ByTool {
		tools = append(tools, g.Key)
	}
	slices.Sort(tools)
	sources := make([]string, 0, len(period.BySource))
	for _, g := range period.BySource {
		sources = append(sources, g.Key)
	}
	slices.Sort(sources)
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

type tempPoint struct {
	Time  time.Time `json:"time"`
	TempC float64   `json:"temp_c"`
}

type aqiPoint struct {
	Time time.Time `json:"time"`
	AQI  float64   `json:"european_aqi"`
}

type weatherCurrentOut struct {
	FetchedAt     time.Time   `json:"fetched_at"`
	Stale         bool        `json:"stale"`
	Condition     string      `json:"condition"`
	ConditionCode *string     `json:"condition_code"`
	Severe        bool        `json:"severe"`
	TempC         float64     `json:"temp_c"`
	Hourly        []tempPoint `json:"hourly"`
}

type airOut struct {
	FetchedAt time.Time  `json:"fetched_at"`
	Stale     bool       `json:"stale"`
	AQI       float64    `json:"european_aqi"`
	PM25      float64    `json:"pm2_5_ugm3"`
	PM10      float64    `json:"pm10_ugm3"`
	Hourly    []aqiPoint `json:"hourly"`
}

type sunOut struct {
	Sunrise time.Time `json:"sunrise"`
	Sunset  time.Time `json:"sunset"`
}

type weatherStateOut struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	Enabled      bool               `json:"enabled"`
	Provider     string             `json:"provider"`
	Units        string             `json:"units"`
	LocationName *string            `json:"location_name"`
	Current      *weatherCurrentOut `json:"current"`
	Air          *airOut            `json:"air"`
	Sun          *sunOut            `json:"sun"`
}

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

	wx := a.weather.state(cfg, now)
	if wx.HaveObs {
		obs := wx.Obs
		cur := &weatherCurrentOut{
			FetchedAt:     wireTime(obs.FetchedAt, loc),
			Stale:         !wx.Fresh,
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
	if wx.HaveAir {
		air := wx.Air
		ao := &airOut{
			FetchedAt: wireTime(air.FetchedAt, loc),
			Stale:     !wx.AirFresh,
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
	if wx.HaveSun {
		rise, set := wx.roundedSun()
		out.Sun = &sunOut{Sunrise: rise.In(loc), Sunset: set.In(loc)}
	}
	return out
}

func (a *App) handleWeatherState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildWeatherState(time.Now()))
}
