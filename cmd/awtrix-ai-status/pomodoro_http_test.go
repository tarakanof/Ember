package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dt/awtrix-ai-status/internal/pomodoro"
)

func newPomodoroApp(t *testing.T) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Pomodoro.Enabled = true
	cfg.applyDefaults()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	eng := pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, realClock{})
	store, err := pomodoro.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	app.EnablePomodoro(eng, store)
	return app
}

func doReq(t *testing.T, srv *httptest.Server, method, path, token, body string) (*http.Response, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	resp.Body.Close()
	return resp, decoded
}

func pomoState(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/state", "", "")
	return body
}

func TestPomodoroStartPauseResumeStopViaHTTP(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if resp, _ := doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", resp.StatusCode)
	}
	st := pomoState(t, srv)
	if st["phase"] != "focus" || st["running"] != true {
		t.Fatalf("after start state = %+v", st)
	}

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/pause", "", "")
	if st := pomoState(t, srv); st["paused"] != true {
		t.Fatalf("after pause state = %+v", st)
	}

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/resume", "", "")
	if st := pomoState(t, srv); st["paused"] != false || st["running"] != true {
		t.Fatalf("after resume state = %+v", st)
	}

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/stop", "", "")
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("after stop state = %+v", st)
	}
}

func TestPomodoroButtonHookMapsPresses(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`)

	press := func(button string, state string) {
		form := url.Values{"button": {button}, "state": {state}, "uid": {"awtrix_test"}}
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("button %s status = %d", button, resp.StatusCode)
		}
	}

	// Middle press toggles pause.
	press("middle", "1")
	if st := pomoState(t, srv); st["paused"] != true {
		t.Fatalf("after middle press state = %+v", st)
	}
	// Release is ignored (state 0).
	press("middle", "0")
	if st := pomoState(t, srv); st["paused"] != true {
		t.Fatalf("release should not change state = %+v", st)
	}
	// Middle again resumes.
	press("middle", "1")
	if st := pomoState(t, srv); st["paused"] != false {
		t.Fatalf("second middle press should resume = %+v", st)
	}
	// Left stops.
	press("left", "1")
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("left press should stop = %+v", st)
	}
}

func TestPomodoroAuthBoundaries(t *testing.T) {
	app := newPomodoroApp(t)
	// Force a token so /v1/ requires auth.
	cfg := *app.cfg.Load()
	cfg.Auth.StatusToken = "secret"
	app.cfg.Store(&cfg)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// Write endpoint without token → 401.
	if resp, _ := doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("start without token = %d, want 401", resp.StatusCode)
	}
	// Read state is open.
	if resp, _ := doReq(t, srv, http.MethodGet, "/v1/pomodoro/state", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("state without token = %d, want 200", resp.StatusCode)
	}
	// Button hook is open (device cannot send a bearer token).
	form := url.Values{"button": {"middle"}, "state": {"1"}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("button hook = %d, want 200 (unauthenticated)", resp.StatusCode)
	}
}

func TestPomodoroConfigPutPersists(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	put := `{"focus_minutes":30,"short_break_minutes":6,"long_break_minutes":20,"rounds_before_long_break":3,"auto_start_next":true,"sound":false,"focus_color":"#112233","break_color":"#445566"}`
	if resp, _ := doReq(t, srv, http.MethodPut, "/v1/pomodoro/config", "", put); resp.StatusCode != http.StatusOK {
		t.Fatalf("put config status = %d", resp.StatusCode)
	}
	_, got := doReq(t, srv, http.MethodGet, "/v1/pomodoro/config", "", "")
	if got["focus_minutes"] != float64(30) || got["focus_color"] != "#112233" || got["auto_start_next"] != true {
		t.Fatalf("config after put = %+v", got)
	}
	// New focus duration should be reflected in a freshly started phase.
	doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`)
	if st := pomoState(t, srv); st["planned_sec"] != float64(30*60) {
		t.Fatalf("planned after config change = %v, want 1800", st["planned_sec"])
	}
}

func TestPomodoroStatsEndpointReflectsStore(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	now := time.Now()
	if err := app.store.RecordPhase(pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed"}, now.Add(-25*time.Minute), now); err != nil {
		t.Fatalf("record: %v", err)
	}
	_, body := doReq(t, srv, http.MethodGet, "/v1/pomodoro/stats", "", "")
	today, ok := body["today"].(map[string]any)
	if !ok {
		t.Fatalf("stats today missing: %+v", body)
	}
	if today["completed_focus"] != float64(1) {
		t.Fatalf("today completed_focus = %v, want 1", today["completed_focus"])
	}
	if body["streak"] != float64(1) {
		t.Fatalf("streak = %v, want 1", body["streak"])
	}
}
