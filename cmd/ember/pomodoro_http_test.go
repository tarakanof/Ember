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

// TestPomoPhaseEndAlertUsesNGKeys pins the ad-hoc phase-end notification on
// awtrix-ng's schema: durationMs in milliseconds and soundRtttl for the inline
// melody (AWTRIX3's `duration`/`rtttl` are 422s on NG).
func TestPomoPhaseEndAlertUsesNGKeys(t *testing.T) {
	cases := []struct {
		name      string
		melody    string
		wantKey   string
		wantValue string
	}{
		{"default inline melody", "", "soundRtttl", defaultPomoMelody},
		{"configured device melody", "chime", "sound", "chime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := &recordingPublisher{}
			cfg := defaultConfig()
			cfg.Pomodoro.Sound = true
			cfg.Pomodoro.SoundMelody = tc.melody
			app := NewApp(cfg, pub, testLogger())
			app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})

			notes := pub.NotifySnapshot()
			if len(notes) != 1 {
				t.Fatalf("expected 1 notification, got %d", len(notes))
			}
			p := notes[0]
			if p["text"] != "BREAK" {
				t.Errorf("text = %v, want BREAK", p["text"])
			}
			if p["durationMs"] != 4000 {
				t.Errorf("durationMs = %v, want 4000", p["durationMs"])
			}
			if _, has := p["duration"]; has {
				t.Error(`legacy "duration" present — NG rejects it`)
			}
			if _, has := p["rtttl"]; has {
				t.Error(`legacy "rtttl" present — NG renamed it soundRtttl`)
			}
			if p[tc.wantKey] != tc.wantValue {
				t.Errorf("%s = %v, want %v", tc.wantKey, p[tc.wantKey], tc.wantValue)
			}
		})
	}
}

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
	// Left stops immediately on press; release is a no-op.
	press("left", "1")
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("left press should stop immediately = %+v", st)
	}
	press("left", "0")
	if st := pomoState(t, srv); st["phase"] != "idle" {
		t.Fatalf("left release should not change state = %+v", st)
	}
}

// TestPomodoroButtonRightSkipsOnPress pins right=skip firing on the press
// edge (state=1), with no chord left to pre-empt it.
func TestPomodoroButtonRightSkipsOnPress(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	press := buttonPresser(t, srv)

	doReq(t, srv, http.MethodPost, "/v1/pomodoro/start", "", `{"phase":"focus"}`)

	press(url.Values{"button": {"right"}, "state": {"1"}, "uid": {"awtrix_test"}})
	if st := pomoState(t, srv); st["phase"] != "short_break" {
		t.Fatalf("right press should skip focus into short_break immediately, got %+v", st)
	}
	// Release is a no-op.
	press(url.Values{"button": {"right"}, "state": {"0"}, "uid": {"awtrix_test"}})
	if st := pomoState(t, srv); st["phase"] != "short_break" {
		t.Fatalf("right release should not change state, got %+v", st)
	}
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

// buttonPresser posts an awtrix-ng button callback exactly as the firmware does:
// form-encoded button/state/uid over plain HTTP, one call per edge.
func buttonPresser(t *testing.T, srv *httptest.Server) func(form url.Values) int {
	t.Helper()
	return func(form url.Values) int {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/hooks/awtrix/button", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
}

// NG names the centre button "middle"; MQTT, Berry scripts and AWTRIX3 all call
// the same button "select", so both spellings must drive the timer.
func TestAwtrixButtonAcceptsMiddleAndSelectSpellings(t *testing.T) {
	for _, name := range []string{"middle", "select"} {
		t.Run(name, func(t *testing.T) {
			app := newPomodoroApp(t)
			srv := httptest.NewServer(app.routes())
			defer srv.Close()
			press := buttonPresser(t, srv)

			if code := press(url.Values{"button": {name}, "state": {"1"}, "uid": {"e868e705ffb8"}}); code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if st := pomoState(t, srv); st["phase"] != "focus" || st["running"] != true {
				t.Fatalf("%s press from idle should start focus, got %+v", name, st)
			}
		})
	}
}

// The uid the firmware attaches (its MAC, so several panels can share one
// endpoint) must neither be required nor change the mapping.
func TestAwtrixButtonIgnoresUID(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
	}{
		{"no uid at all", url.Values{"button": {"middle"}, "state": {"1"}}},
		{"foreign panel uid", url.Values{"button": {"middle"}, "state": {"1"}, "uid": {"aabbccddeeff"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newPomodoroApp(t)
			srv := httptest.NewServer(app.routes())
			defer srv.Close()

			if code := buttonPresser(t, srv)(tc.form); code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if st := pomoState(t, srv); st["running"] != true {
				t.Fatalf("press should have started the timer, got %+v", st)
			}
		})
	}
}

// An unknown button name (a future firmware, or a swapped/rotated panel naming
// scheme we don't know) is accepted and ignored — the callback is
// fire-and-forget, so a non-200 would only cost the device stutter.
func TestAwtrixButtonIgnoresUnknownButton(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if code := buttonPresser(t, srv)(url.Values{"button": {"top"}, "state": {"1"}}); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if st := pomoState(t, srv); st["running"] == true {
		t.Fatalf("unknown button should not start the timer, got %+v", st)
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
		FocusMinutes: intPtr(30), ShortBreakMinutes: intPtr(5), LongBreakMinutes: intPtr(15), RoundsBeforeLongBreak: intPtr(4),
		FocusColor: strPtr("#FF3B30"), BreakColor: strPtr("#2EE85E"),
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
