package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

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

func (a *App) statsLoc() *time.Location { return time.Local }

func (a *App) statsNow() time.Time {
	if a.statsClock != nil {
		return a.statsClock()
	}
	return time.Now()
}

const activityThrottle = 2 * time.Minute

const activitySweepInterval = time.Hour

const activityRetention = 400 * 24 * time.Hour

func activeWorkState(state string) bool {
	switch state {
	case "running", "waiting", "error":
		return true
	default:
		return false
	}
}

const activityWaitFloor = 10 * time.Second

type activityMark struct {
	at    time.Time
	state string
}

func (a *App) recordActivityHeartbeat(s Session, now time.Time) {
	a.sourceColors.remember(s.Source, s.SourceColor)
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

func (a *App) loadPhaseRecords(now time.Time, days int) ([]pomodoro.PhaseRecord, error) {
	lo := now.AddDate(0, 0, -days)
	hi := now.Add(time.Minute)
	return a.store.PhasesBetween(lo, hi)
}

type goalStatus struct {
	DailySessions  int  `json:"daily_sessions"`
	TodayCompleted int  `json:"today_completed"`
	DailyMet       bool `json:"daily_met"`
	WeeklyDays     int  `json:"weekly_days"`
	WeekActiveDays int  `json:"week_active_days"`
	WeeklyMet      bool `json:"weekly_met"`
}

type pomodoroStats struct {
	Today         pomodoro.DayStat        `json:"today"`
	History       []pomodoro.DayStat      `json:"history"`
	Streak        int                     `json:"streak"`
	LongestStreak int                     `json:"longest_streak"`
	Completion    pomodoro.CompletionStat `json:"completion"`
	Goal          goalStatus              `json:"goal"`
	Weekly        []pomodoro.Bucket       `json:"weekly"`
}

func (a *App) buildStats(now time.Time) (pomodoroStats, error) {
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	recs, err := a.loadPhaseRecords(now, 400)
	if err != nil {
		return pomodoroStats{}, err
	}

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

	active := pomodoro.ActiveFocusDays(recs, p.DayStartHour, loc)
	streaks := pomodoro.Streaks(active, now, p.DayStartHour, p.StreakGraceDays)

	cutoff := now.AddDate(0, 0, -30)
	var recent []pomodoro.PhaseRecord
	for _, r := range recs {
		if r.EndedAt.After(cutoff) {
			recent = append(recent, r)
		}
	}
	completion := pomodoro.CompletionStats(recent)

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

const statsCacheTTL = time.Minute

type statsCache struct {
	mu      sync.Mutex
	store   *pomodoro.Store
	gen     uint64
	cfg     PomodoroConfig
	day     string
	expires time.Time
	val     pomodoroStats
}

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

func logicalDayKey(t time.Time, dayStartHour int, loc *time.Location) string {
	return t.In(loc).Add(-time.Duration(dayStartHour) * time.Hour).Format("2006-01-02")
}

func (a *App) handlePomodoroStats(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	stats, err := a.cachedStats(a.statsNow())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

type pomodoroHeatmap struct {
	Grid     [7][24]int        `json:"grid"`
	Calendar []pomodoro.Bucket `json:"calendar"`
	Days     int               `json:"days"`
}

func (a *App) handlePomodoroHeatmap(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	days := clampQueryInt(r, "days", 84, 7, 366)
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	recs, err := a.loadPhaseRecords(a.statsNow(), days)
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

const activitySpanGap = 5 * time.Minute

type pomodoroWorkHours struct {
	Days            []pomodoro.DaySummary `json:"days"`
	GapMin          int                   `json:"gap_min"`
	IncludeActivity bool                  `json:"include_activity"`
}

func (a *App) handlePomodoroWorkHours(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	days := clampQueryInt(r, "days", 14, 1, 90)
	p := a.cfg.Load().Pomodoro
	loc := a.statsLoc()
	now := a.statsNow()
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
