package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// clampQueryInt reads an integer query parameter, falling back to def and
// clamping to [lo, hi].
func clampQueryInt(r *http.Request, key string, def, lo, hi int) int {
	v := def
	if s := r.URL.Query().Get(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			v = n
		}
	}
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

// This file serves the richer Pomodoro statistics that back the dashboard. All
// views are computed in Go from a single window of phase rows so they share one
// consistent notion of "day" (the configured day_start_hour) and one DB read.

// statsLoc is the location used for all day/hour bucketing — the server's local
// zone, matching how the engine stamps phases.
func (a *App) statsLoc() *time.Location { return time.Local }

// loadPhaseRecords fetches the phase rows ending within the last `days` days.
func (a *App) loadPhaseRecords(now time.Time, days int) ([]pomodoro.PhaseRecord, error) {
	lo := now.AddDate(0, 0, -days)
	hi := now.Add(time.Minute) // inclusive of the current instant
	return a.store.PhasesBetween(lo, hi)
}

// goalStatus reports progress toward the daily and weekly goals. A goal of 0
// means "disabled" and reports met=true (nothing to fail).
type goalStatus struct {
	DailySessions  int  `json:"daily_sessions"`
	TodayCompleted int  `json:"today_completed"`
	DailyMet       bool `json:"daily_met"`
	WeeklyDays     int  `json:"weekly_days"`
	WeekActiveDays int  `json:"week_active_days"`
	WeeklyMet      bool `json:"weekly_met"`
}

// pomodoroStats is the wire shape for GET /v1/pomodoro/stats. The first three
// fields preserve the original response; the rest are additive.
type pomodoroStats struct {
	Today         pomodoro.DayStat        `json:"today"`
	History       []pomodoro.DayStat      `json:"history"`
	Streak        int                     `json:"streak"`
	LongestStreak int                     `json:"longest_streak"`
	Completion    pomodoro.CompletionStat `json:"completion"`
	Goal          goalStatus              `json:"goal"`
	Weekly        []pomodoro.Bucket       `json:"weekly"`
}

// buildStats assembles the full statistics payload as of `now`.
func (a *App) buildStats(now time.Time) (pomodoroStats, error) {
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	recs, err := a.loadPhaseRecords(now, 400)
	if err != nil {
		return pomodoroStats{}, err
	}

	// Per-day completed-focus rollup → today + 7-day history (logical days).
	daily := make(map[string]pomodoro.Bucket)
	for _, b := range pomodoro.Rollup(recs, pomodoro.GranDay, p.DayStartHour, loc) {
		daily[b.Key] = b
	}
	dayStat := func(t time.Time) pomodoro.DayStat {
		key := logicalDayKey(t, p.DayStartHour, loc)
		b := daily[key]
		return pomodoro.DayStat{Date: key, CompletedFocus: b.Sessions, FocusMin: b.FocusMin}
	}
	history := make([]pomodoro.DayStat, 0, 7)
	for i := 0; i < 7; i++ {
		history = append(history, dayStat(now.AddDate(0, 0, -i)))
	}
	today := history[0]

	// Streaks (grace-aware) over the whole window.
	active := pomodoro.ActiveFocusDays(recs, p.DayStartHour, loc)
	streaks := pomodoro.Streaks(active, now, p.DayStartHour, p.StreakGraceDays)

	// Completion over the last 30 days.
	cutoff := now.AddDate(0, 0, -30)
	var recent []pomodoro.PhaseRecord
	for _, r := range recs {
		if r.EndedAt.After(cutoff) {
			recent = append(recent, r)
		}
	}
	completion := pomodoro.CompletionStats(recent)

	// Goals.
	weekActive := 0
	for i := 0; i < 7; i++ {
		if active[logicalDayKey(now.AddDate(0, 0, -i), p.DayStartHour, loc)] {
			weekActive++
		}
	}
	goal := goalStatus{
		DailySessions:  p.DailyGoalSessions,
		TodayCompleted: today.CompletedFocus,
		DailyMet:       p.DailyGoalSessions == 0 || today.CompletedFocus >= p.DailyGoalSessions,
		WeeklyDays:     p.WeeklyGoalDays,
		WeekActiveDays: weekActive,
		WeeklyMet:      p.WeeklyGoalDays == 0 || weekActive >= p.WeeklyGoalDays,
	}

	// Weekly trend: last 12 ISO-week buckets.
	weekly := pomodoro.Rollup(recs, pomodoro.GranWeek, p.DayStartHour, loc)
	if len(weekly) > 12 {
		weekly = weekly[len(weekly)-12:]
	}

	return pomodoroStats{
		Today:         today,
		History:       history,
		Streak:        streaks.Current,
		LongestStreak: streaks.Longest,
		Completion:    completion,
		Goal:          goal,
		Weekly:        weekly,
	}, nil
}

// logicalDayKey mirrors pomodoro's internal day bucketing for handler-side use.
func logicalDayKey(t time.Time, dayStartHour int, loc *time.Location) string {
	return t.In(loc).Add(-time.Duration(dayStartHour) * time.Hour).Format("2006-01-02")
}

// handlePomodoroStats serves GET /v1/pomodoro/stats (rich payload).
func (a *App) handlePomodoroStats(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	stats, err := a.buildStats(time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// pomodoroHeatmap is the wire shape for GET /v1/pomodoro/heatmap.
type pomodoroHeatmap struct {
	Grid     [7][24]int        `json:"grid"`     // [weekday 0=Sun][hour] completed-focus minutes
	Calendar []pomodoro.Bucket `json:"calendar"` // per-day completed focus, chronological
	Days     int               `json:"days"`
}

// handlePomodoroHeatmap serves GET /v1/pomodoro/heatmap?days=84 — the
// weekday×hour grid plus a daily calendar series for the consistency heatmap.
func (a *App) handlePomodoroHeatmap(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	days := clampQueryInt(r, "days", 84, 7, 366)
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	recs, err := a.loadPhaseRecords(time.Now(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, pomodoroHeatmap{
		Grid:     pomodoro.WeekdayHourHeatmap(recs, loc),
		Calendar: pomodoro.Rollup(recs, pomodoro.GranDay, p.DayStartHour, loc),
		Days:     days,
	})
}

// pomodoroWorkHours is the wire shape for GET /v1/pomodoro/workhours.
type pomodoroWorkHours struct {
	Days   []pomodoro.DaySummary `json:"days"` // most-recent first
	GapMin int                   `json:"gap_min"`
}

// handlePomodoroWorkHours serves GET /v1/pomodoro/workhours?days=14 — the
// sessionized work-hours summary per logical day.
func (a *App) handlePomodoroWorkHours(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	days := clampQueryInt(r, "days", 14, 1, 90)
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	now := time.Now()
	recs, err := a.loadPhaseRecords(now, days+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]pomodoro.DaySummary, 0, days)
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -i)
		summary := pomodoro.DayWork(recs, day, p.workHoursGap(), p.DayStartHour, loc)
		// DayWork anchors WorkEnd to the last block; for the current day, leave it
		// as-is (last activity) rather than "now" to avoid an open-ended span.
		out = append(out, summary)
	}
	writeJSON(w, http.StatusOK, pomodoroWorkHours{Days: out, GapMin: int(p.workHoursGap() / time.Minute)})
}
