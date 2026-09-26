package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// recFocus inserts a focus phase ending at `ended` lasting durMin minutes.
func recFocus(t *testing.T, app *App, ended time.Time, durMin int, completed bool, reason string) {
	t.Helper()
	res := pomodoro.PhaseResult{
		Phase: pomodoro.PhaseFocus, PlannedSec: durMin * 60, ActualSec: durMin * 60,
		Completed: completed, Reason: reason,
	}
	start := ended.Add(-time.Duration(durMin) * time.Minute)
	if err := app.store.RecordPhase(res, start, ended); err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestPomodoroStatsRichPayload(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	now := time.Now()

	// Three focus phases today: two completed, one abandoned.
	recFocus(t, app, now.Add(-10*time.Minute), 25, true, "completed")
	recFocus(t, app, now.Add(-45*time.Minute), 25, true, "completed")
	recFocus(t, app, now.Add(-80*time.Minute), 8, false, "stopped")
	// One completed yesterday, to extend the streak.
	recFocus(t, app, now.AddDate(0, 0, -1), 25, true, "completed")

	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/stats", "", "")

	today := body["today"].(map[string]any)
	if today["completed_focus"].(float64) != 2 {
		t.Errorf("today completed = %v, want 2", today["completed_focus"])
	}
	if body["streak"].(float64) < 2 {
		t.Errorf("streak = %v, want >= 2", body["streak"])
	}
	// Completion spans 30 days: 3 completed (2 today + 1 yesterday) + 1 abandoned.
	comp := body["completion"].(map[string]any)
	if comp["completed_focus"].(float64) != 3 || comp["abandoned_focus"].(float64) != 1 {
		t.Errorf("completion = %+v", comp)
	}
	if r := comp["completion_rate"].(float64); r < 0.74 || r > 0.76 {
		t.Errorf("completion_rate = %v, want 0.75", r)
	}
	goal := body["goal"].(map[string]any)
	if goal["daily_sessions"].(float64) != 8 || goal["today_completed"].(float64) != 2 {
		t.Errorf("goal = %+v", goal)
	}
	if goal["daily_met"].(bool) {
		t.Errorf("daily goal should not be met with 2/8")
	}
	if _, ok := body["weekly"]; !ok {
		t.Errorf("missing weekly buckets")
	}
}

// TestPomodoroStatsCachedUntilPhaseWrite asserts repeated stats polls are
// served from cache (a row the app's store never wrote stays invisible) and
// that a phase recorded through the app's store shows up on the next poll.
func TestPomodoroStatsCachedUntilPhaseWrite(t *testing.T) {
	app := newPomodoroApp(t)
	path := filepath.Join(t.TempDir(), "shared.db")
	own, err := pomodoro.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { own.Close() })
	app.store = own
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	now := time.Now()
	todayCompleted := func() float64 {
		t.Helper()
		_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/stats", "", "")
		return body["today"].(map[string]any)["completed_focus"].(float64)
	}

	recFocus(t, app, now.Add(-10*time.Minute), 25, true, "completed")
	if got := todayCompleted(); got != 1 {
		t.Fatalf("first poll: completed = %v, want 1", got)
	}

	// A second handle on the same file writes behind the app's back: the
	// app's cache has no reason to drop, so the poll must not re-query.
	other, err := pomodoro.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	res := pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed"}
	if err := other.RecordPhase(res, now.Add(-60*time.Minute), now.Add(-35*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := todayCompleted(); got != 1 {
		t.Fatalf("cached poll: completed = %v, want 1 (served from cache)", got)
	}

	// A write through the app's own store invalidates the cache.
	recFocus(t, app, now.Add(-5*time.Minute), 1, true, "completed")
	if got := todayCompleted(); got != 3 {
		t.Fatalf("after app write: completed = %v, want 3", got)
	}
}

// statsBehindTheBack returns an app whose store shares a file with a second
// handle; phases written through that handle do not invalidate the cache, so
// they only show up when the cache is rebuilt for another reason.
func statsBehindTheBack(t *testing.T) (*App, *pomodoro.Store) {
	t.Helper()
	app := newPomodoroApp(t)
	path := filepath.Join(t.TempDir(), "shared.db")
	own, err := pomodoro.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { own.Close() })
	app.store = own
	other, err := pomodoro.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	return app, other
}

func completedIn30d(t *testing.T, app *App, now time.Time) int {
	t.Helper()
	s, err := app.cachedStats(now)
	if err != nil {
		t.Fatal(err)
	}
	return s.Completion.CompletedFocus
}

func writeFocusVia(t *testing.T, st *pomodoro.Store, ended time.Time) {
	t.Helper()
	res := pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed"}
	if err := st.RecordPhase(res, ended.Add(-25*time.Minute), ended); err != nil {
		t.Fatal(err)
	}
}

// TestPomodoroStatsCacheRebuildsOnLogicalDayRollover asserts crossing
// day_start_hour rebuilds the cache even with no phase write in between.
func TestPomodoroStatsCacheRebuildsOnLogicalDayRollover(t *testing.T) {
	app, other := statsBehindTheBack(t)
	startHour := app.cfg.Load().Pomodoro.DayStartHour
	y, m, d := time.Now().AddDate(0, 0, -1).Date()
	boundary := time.Date(y, m, d, startHour, 0, 0, 0, time.Local)
	before, after := boundary.Add(-10*time.Second), boundary.Add(10*time.Second)

	if got := completedIn30d(t, app, before); got != 0 {
		t.Fatalf("before rollover: %d, want 0", got)
	}
	writeFocusVia(t, other, before.Add(-time.Hour))
	if got := completedIn30d(t, app, before.Add(5*time.Second)); got != 0 {
		t.Fatalf("same logical day: %d, want 0 (cached)", got)
	}
	if got := completedIn30d(t, app, after); got != 1 {
		t.Errorf("after rollover: %d, want 1 (rebuilt)", got)
	}
}

// TestPomodoroStatsCacheExpiresAfterTTL asserts the cache is rebuilt once
// statsCacheTTL has passed, which is how writes by another store handle or
// process become visible.
func TestPomodoroStatsCacheExpiresAfterTTL(t *testing.T) {
	app, other := statsBehindTheBack(t)
	y, m, d := time.Now().AddDate(0, 0, -1).Date()
	t0 := time.Date(y, m, d, 12, 0, 0, 0, time.Local) // clear of any day boundary

	if got := completedIn30d(t, app, t0); got != 0 {
		t.Fatalf("first build: %d, want 0", got)
	}
	writeFocusVia(t, other, t0.Add(-time.Hour))
	if got := completedIn30d(t, app, t0.Add(statsCacheTTL-time.Second)); got != 0 {
		t.Fatalf("within TTL: %d, want 0 (cached)", got)
	}
	if got := completedIn30d(t, app, t0.Add(statsCacheTTL+time.Second)); got != 1 {
		t.Errorf("after TTL: %d, want 1 (rebuilt)", got)
	}
}

// TestPomodoroStatsCacheFollowsConfig asserts a goal change is reflected
// immediately rather than after the cache expires.
func TestPomodoroStatsCacheFollowsConfig(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/stats", "", "")
	if got := body["goal"].(map[string]any)["daily_sessions"].(float64); got != 8 {
		t.Fatalf("daily_sessions = %v, want 8", got)
	}
	app.updateConfig(func(c *Config) { c.Pomodoro.DailyGoalSessions = 3 })
	_, body = doReq(t, srv, http.MethodGet, "/v1/pomodoro/stats", "", "")
	if got := body["goal"].(map[string]any)["daily_sessions"].(float64); got != 3 {
		t.Fatalf("daily_sessions after config change = %v, want 3", got)
	}
}

func TestPomodoroHeatmapEndpoint(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	recFocus(t, app, time.Now().Add(-30*time.Minute), 25, true, "completed")

	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/heatmap?days=84", "", "")
	grid, ok := body["grid"].([]any)
	if !ok || len(grid) != 7 {
		t.Fatalf("grid should be 7 rows, got %v", body["grid"])
	}
	if len(grid[0].([]any)) != 24 {
		t.Errorf("grid row should be 24 hours")
	}
	if _, ok := body["calendar"].([]any); !ok {
		t.Errorf("missing calendar series")
	}
}

func TestPomodoroWorkHoursEndpoint(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	now := time.Now()
	recFocus(t, app, now.Add(-10*time.Minute), 25, true, "completed")
	recFocus(t, app, now.Add(-45*time.Minute), 25, true, "completed")

	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/workhours?days=7", "", "")
	days, ok := body["days"].([]any)
	if !ok || len(days) != 7 {
		t.Fatalf("want 7 day summaries, got %v", body["days"])
	}
	if body["gap_min"].(float64) != 15 {
		t.Errorf("gap_min = %v, want 15", body["gap_min"])
	}
	d0 := days[0].(map[string]any)
	if d0["active_sec"].(float64) <= 0 {
		t.Errorf("today active_sec should be > 0, got %v", d0["active_sec"])
	}
}

func TestPomodoroWorkHoursOverlay(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	now := time.Now()

	// A 25-min focus block, then AI activity for ~10 min after it (10-min gap →
	// same work session). Overlay active = 25m focus + 10m activity = 35m.
	recFocus(t, app, now.Add(-40*time.Minute), 25, true, "completed") // [-65m, -40m]
	for tm := now.Add(-30 * time.Minute); !tm.After(now.Add(-20 * time.Minute)); tm = tm.Add(2 * time.Minute) {
		if err := app.store.RecordActivity(tm, "Claude", "claude", "Claude/claude/s1", "running"); err != nil {
			t.Fatal(err)
		}
	}

	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/workhours?days=2", "", "")
	if body["include_activity"] != true {
		t.Fatalf("include_activity = %v, want true", body["include_activity"])
	}
	d0 := body["days"].([]any)[0].(map[string]any)
	active := d0["active_sec"].(float64)
	// Must exceed focus-only (25m) because the post-focus activity span adds ~10m.
	if active < 34*60 {
		t.Errorf("overlay active_sec = %v, want ~2100 (35m incl. activity)", active)
	}
}

func TestStatusRecordsActivityThrottled(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	post := func(state string) {
		body := `{"source":"Claude","tool":"claude","session":"s1","state":"` + state + `"}`
		if resp, _ := doReq(t, srv, http.MethodPost, "/v1/status", "", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("status post %s = %d", state, resp.StatusCode)
		}
	}
	post("running") // records
	post("running") // throttled (within 2m window) → no new row
	post("idle")    // not an active state → no row

	rows, err := app.store.ActivityBetween(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 throttled activity row, got %d", len(rows))
	}
	if rows[0].SessionKey != "Claude/claude/s1" || rows[0].State != "running" {
		t.Errorf("row = %+v", rows[0])
	}

	// Disabling the overlay stops recording.
	cfg := *app.cfg.Load()
	cfg.Pomodoro.WorkHoursIncludeActivity = false
	app.cfg.Store(&cfg)
	app.activityMu.Lock()
	delete(app.activityLast, "Claude/claude/s1") // clear throttle so a row could be written
	app.activityMu.Unlock()
	post("running")
	rows, _ = app.store.ActivityBetween(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if len(rows) != 1 {
		t.Fatalf("overlay disabled should not record; got %d rows", len(rows))
	}
}

func TestPomodoroDashboardServesHTML(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/pomodoro/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Pomodoro") || !strings.Contains(string(b), "getJSON") {
		t.Errorf("dashboard HTML looks wrong (len %d)", len(b))
	}
}

// TestActivityHeartbeatBoundsGrowth asserts the per-session throttle map drops
// entries that can no longer throttle anything, and that heartbeats prune
// activity rows older than the retention window.
func TestActivityHeartbeatBoundsGrowth(t *testing.T) {
	app := newPomodoroApp(t)
	now := time.Now()
	ancient := now.Add(-activityRetention - time.Hour)
	if err := app.store.RecordActivity(ancient, "Claude", "claude", "Claude/claude/old", "running"); err != nil {
		t.Fatal(err)
	}

	app.recordActivityHeartbeat(Session{Source: "Claude", Tool: "claude", Session: "s1", State: "running"}, now)
	later := now.Add(activitySweepInterval + time.Minute)
	app.recordActivityHeartbeat(Session{Source: "Claude", Tool: "claude", Session: "s2", State: "running"}, later)

	app.activityMu.Lock()
	_, stale := app.activityLast["Claude/claude/s1"]
	_, fresh := app.activityLast["Claude/claude/s2"]
	app.activityMu.Unlock()
	if stale || !fresh {
		t.Errorf("activityLast: s1 kept=%v (want false), s2 kept=%v (want true)", stale, fresh)
	}

	rows, err := app.store.ActivityBetween(ancient.Add(-time.Hour), ancient.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("activity rows older than retention: got %d, want 0", len(rows))
	}
}
