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

	"github.com/tarakanof/ember/internal/pomodoro"
)

func newPomodoroApp(t *testing.T) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Pomodoro.Enabled = true
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
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
	// An empty token means "use the default test token" so write endpoints
	// (fail-closed) are reachable; auth-boundary tests pass an explicit token.
	if token == "" {
		token = testToken
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
	// Left stops — on release, so a chord can pre-empt it. The press alone is a no-op.
	press("left", "1")
	if st := pomoState(t, srv); st["phase"] == "idle" {
		t.Fatalf("left press-down alone should not stop yet = %+v", st)
	}
	press("left", "0")
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("left release should stop = %+v", st)
	}
}

func TestPomodoroButtonChordToggles(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	press := func(button, state string) {
		form := url.Values{"button": {button}, "state": {state}, "uid": {"awtrix_test"}}
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// From idle: a left+right chord starts a focus block.
	press("left", "1")
	press("right", "1") // both held → chord fires
	if st := pomoState(t, srv); st["phase"] != "focus" || st["running"] != true {
		t.Fatalf("chord from idle should start focus, got %+v", st)
	}
	// Releasing the chord must NOT fire the single left=stop / right=skip actions.
	press("left", "0")
	press("right", "0")
	if st := pomoState(t, srv); st["phase"] != "focus" {
		t.Fatalf("chord release should be suppressed, got %+v", st)
	}

	// While focused: another chord stops (the "disable" toggle).
	press("right", "1")
	press("left", "1") // both held again → chord
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("chord while focused should stop, got %+v", st)
	}
	press("right", "0")
	press("left", "0")
}

func TestAwtrixButtonHeldReminderSuppressesPomodoro(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`)
	// Arm a held reminder window.
	app.reminderHeldUntil.Store(time.Now().Add(time.Minute).UnixNano())

	press := func(button string) {
		form := url.Values{"button": {button}, "state": {"1"}, "uid": {"awtrix_test"}}
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// While held, the middle press acknowledges the reminder — Pomodoro must NOT pause.
	press("middle")
	if st := pomoState(t, srv); st["paused"] == true {
		t.Fatalf("held middle press should not pause Pomodoro = %+v", st)
	}
	// The middle press disarmed the window and dismissed the on-clock notification.
	if app.reminderHeldUntil.Load() != 0 {
		t.Fatal("middle press should disarm reminderHeldUntil")
	}
	if rp, ok := app.publisher.(*recordingPublisher); ok {
		rp.mu.Lock()
		d := rp.dismissals
		rp.mu.Unlock()
		if d != 1 {
			t.Fatalf("middle press should dismiss the notification once, got %d", d)
		}
	}
	press("middle")
	if st := pomoState(t, srv); st["paused"] != true {
		t.Fatalf("after disarm, middle press should pause = %+v", st)
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

	// Write endpoint with a non-matching token → 401.
	if resp, _ := doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "wrong", `{"phase":"focus"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("start with wrong token = %d, want 401", resp.StatusCode)
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

func TestPomodoroButtonStartsFromIdle(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// Engine is idle. A middle press should begin a focus block.
	form := url.Values{"button": {"middle"}, "state": {"1"}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if st := pomoState(t, srv); st["phase"] != "focus" || st["running"] != true {
		t.Fatalf("middle-from-idle should start focus, got %+v", st)
	}
}

func TestPomodoroConfigPutRoundTripsCap(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	put := `{"focus_minutes":50,"short_break_minutes":5,"long_break_minutes":15,` +
		`"rounds_before_long_break":4,"auto_start_next":true,"sound":false,` +
		`"focus_color":"#112233","break_color":"#445566","max_session_minutes":120}`
	if resp, _ := doReq(t, srv, http.MethodPut, "/v1/pomodoro/config", "", put); resp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d", resp.StatusCode)
	}
	_, got := doReq(t, srv, http.MethodGet, "/v1/pomodoro/config", "", "")
	if got["max_session_minutes"] != float64(120) || got["focus_minutes"] != float64(50) {
		t.Fatalf("config round-trip = %+v", got)
	}
}

func TestResyncPomodoroAfterReloadKeepsPersistedEdits(t *testing.T) {
	app := newPomodoroApp(t)

	// Persist a runtime edit (focus=30) via the API path.
	if err := app.applyPomodoroSettings(pomodoroSettingsDTO{
		FocusMinutes: 30, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4,
		FocusColor: "#FF3B30", BreakColor: "#2EE85E",
	}); err != nil {
		t.Fatalf("applyPomodoroSettings: %v", err)
	}

	// Simulate a config reload that resets the file's pomodoro block to 25/engine untouched.
	cfg := *app.cfg.Load()
	cfg.Pomodoro.FocusMinutes = 25
	app.cfg.Store(&cfg)

	app.resyncPomodoroAfterReload()

	if got := app.cfg.Load().Pomodoro.FocusMinutes; got != 30 {
		t.Fatalf("cfg focus after reload+resync = %d, want 30 (persisted edit)", got)
	}
	if got := app.engine.CurrentSettings().FocusMin; got != 30 {
		t.Fatalf("engine focus after reload+resync = %d, want 30", got)
	}
}
