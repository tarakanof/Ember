package main

import (
	"net/http"
	"strconv"
	"sync"
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

// activityThrottle bounds how often a session's active state is persisted to the
// activity timeline. Producers heartbeat every 2-10s; the work-hours view only
// needs sub-15-min resolution, so one row per session per window is plenty.
const activityThrottle = 2 * time.Minute

// activitySweepInterval is how often a heartbeat also drops expired entries
// from App.activityLast and prunes old activity rows. Claude session IDs are
// unique per run, so without the sweep the map grows for the process lifetime.
const activitySweepInterval = time.Hour

// activityRetention is how long activity rows are kept. The only reader is
// the work-hours view, which queries at most 91 days back; 400 days leaves a
// wide margin and older rows are never read.
const activityRetention = 400 * 24 * time.Hour

// activeWorkState reports whether a session state counts as "actively working"
// for work-hours purposes. idle and done do not.
func activeWorkState(state string) bool {
	switch state {
	case "running", "waiting", "error":
		return true
	default:
		return false
	}
}

// activityWaitFloor is the minimum gap between two rows of one session even
// when a transition into waiting bypasses the throttle, so a flapping producer
// (or two producers sharing a session key) can't write a row per POST.
const activityWaitFloor = 10 * time.Second

// activityMark is the last activity row persisted for one session.
type activityMark struct {
	at    time.Time
	state string
}

// recordActivityHeartbeat persists an activity row for an actively-working
// session, throttled to one row per session per activityThrottle window, and
// once per activitySweepInterval bounds the throttle map and the table. A
// transition into waiting bypasses the throttle (subject to activityWaitFloor)
// so a short prompt still reaches the activity summary's attention count.
// No-op when the store is absent or the overlay is disabled.
func (a *App) recordActivityHeartbeat(s Session, now time.Time) {
	if a.store == nil || !a.cfg.Load().Pomodoro.WorkHoursIncludeActivity || !activeWorkState(s.State) {
		return
	}
	key := s.Key()
	a.activityMu.Lock()
	if last, ok := a.activityLast[key]; ok {
		elapsed := now.Sub(last.at)
		intoWaiting := s.State == "waiting" && last.state != "waiting"
		if elapsed < activityThrottle && !(intoWaiting && elapsed >= activityWaitFloor) {
			a.activityMu.Unlock()
			return
		}
	}
	a.activityLast[key] = activityMark{at: now, state: s.State}
	sweep := now.Sub(a.activitySweptAt) >= activitySweepInterval
	if sweep {
		a.activitySweptAt = now
		for k, last := range a.activityLast {
			if now.Sub(last.at) >= activityThrottle {
				delete(a.activityLast, k)
			}
		}
	}
	a.activityMu.Unlock()

	if err := a.store.RecordActivity(now, s.Source, s.Tool, key, s.State); err != nil {
		a.logger.Warn("activity record failed", "err", err)
	}
	if sweep {
		if _, err := a.store.PruneActivity(now.Add(-activityRetention)); err != nil {
			a.logger.Warn("activity prune failed", "err", err)
		}
	}
}

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

// statsCacheTTL bounds how long a cached stats payload is served. A phase
// written through App.store, a Pomodoro config change or a logical-day
// rollover drops the cache at once. The TTL covers everything else: what
// drifts with the clock alone (the rolling 30-day completion window) and
// writes that bypass App.store (another process, the sqlite CLI, a restored
// DB file), which become visible only once it expires.
const statsCacheTTL = time.Minute

// statsCache holds the last /v1/pomodoro/stats payload. The menu polls it
// every few seconds, and each build scans ~400 days of phase rows over the
// store's single connection, where it queues behind activity inserts.
type statsCache struct {
	mu      sync.Mutex // protects every field below
	store   *pomodoro.Store
	gen     uint64
	cfg     PomodoroConfig
	day     string
	expires time.Time
	val     pomodoroStats
}

// cachedStats returns buildStats(now), reusing the previous result while the
// phases table, the Pomodoro config and the logical day are unchanged.
func (a *App) cachedStats(now time.Time) (pomodoroStats, error) {
	p := a.cfg.Load().Pomodoro
	day := logicalDayKey(now, p.DayStartHour, a.statsLoc())
	gen := a.store.PhaseGen()
	c := &a.statsCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == a.store && c.gen == gen && c.cfg == p && c.day == day && now.Before(c.expires) {
		return c.val, nil
	}
	val, err := a.buildStats(now)
	if err != nil {
		return pomodoroStats{}, err
	}
	c.store, c.gen, c.cfg, c.day, c.expires, c.val = a.store, gen, p, day, now.Add(statsCacheTTL), val
	return val, nil
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
	stats, err := a.cachedStats(time.Now())
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

// activitySpanGap reconstructs continuous active spans from activity heartbeats:
// heartbeats no more than this apart are one span. Matches the RescueTime-style
// 5-minute idle rule and comfortably exceeds the 2-min recording throttle.
const activitySpanGap = 5 * time.Minute

// pomodoroWorkHours is the wire shape for GET /v1/pomodoro/workhours.
type pomodoroWorkHours struct {
	Days            []pomodoro.DaySummary `json:"days"` // most-recent first
	GapMin          int                   `json:"gap_min"`
	IncludeActivity bool                  `json:"include_activity"` // AI-session activity overlaid onto focus blocks
}

// handlePomodoroWorkHours serves GET /v1/pomodoro/workhours?days=14 — the
// sessionized work-hours summary per logical day. When the overlay is enabled,
// AI-coding-session activity is unioned with the focus blocks.
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
	var acts []pomodoro.ActivityRecord
	if p.WorkHoursIncludeActivity {
		if acts, err = a.store.ActivityBetween(now.AddDate(0, 0, -(days+1)), now.Add(time.Minute)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	out := make([]pomodoro.DaySummary, 0, days)
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -i)
		var summary pomodoro.DaySummary
		if p.WorkHoursIncludeActivity {
			summary = pomodoro.DayWorkOverlay(recs, acts, day, p.workHoursGap(), activitySpanGap, p.DayStartHour, loc)
		} else {
			summary = pomodoro.DayWork(recs, day, p.workHoursGap(), p.DayStartHour, loc)
		}
		out = append(out, summary)
	}
	writeJSON(w, http.StatusOK, pomodoroWorkHours{
		Days: out, GapMin: int(p.workHoursGap() / time.Minute), IncludeActivity: p.WorkHoursIncludeActivity,
	})
}
