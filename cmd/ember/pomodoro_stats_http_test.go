package main

import (
	"io"
	"net/http"
	"net/http/httptest"
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
