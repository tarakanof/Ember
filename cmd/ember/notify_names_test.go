package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/pomodoro"
)

// notifyName returns the "name" key of the nth recorded notification.
func notifyName(t *testing.T, pub *recordingPublisher, n int) string {
	t.Helper()
	all := pub.NotifySnapshot()
	if len(all) <= n {
		t.Fatalf("want at least %d notifications, got %d", n+1, len(all))
	}
	name, _ := all[n]["name"].(string)
	return name
}

func TestReminderPopupIsNamedAndCarriesItsOwnChime(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Stand-up","sound":true,"duration":10,"hold":true}`))
	app.handleReminderFire(httptest.NewRecorder(), req)

	if got := notifyName(t, pub, 0); got != notifyNameReminder {
		t.Errorf("name = %q, want %q", got, notifyNameReminder)
	}
	p := pub.NotifySnapshot()[0]
	if p["soundRtttl"] != defaultReminderSound {
		t.Errorf("soundRtttl = %v, want %q", p["soundRtttl"], defaultReminderSound)
	}
	if got := pub.RTTTLsSnapshot(); len(got) != 0 {
		t.Errorf("chime must ride on the notification, not out-of-band; got %v", got)
	}
}

func TestReminderPopupSilentOmitsSoundKey(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Walk","sound":false}`))
	app.handleReminderFire(httptest.NewRecorder(), req)
	if _, ok := pub.NotifySnapshot()[0]["soundRtttl"]; ok {
		t.Error("sound=false must omit soundRtttl")
	}
}

func TestReminderChimeStrippedDuringQuietHours(t *testing.T) {
	cfg := defaultConfig()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	pub := &recordingPublisher{}
	// Re-wrap with a quiet gate whose window is always active.
	app.publisher = &quietPublisher{
		next: pub,
		cfg: func() *Config {
			c := *app.cfg.Load()
			c.QuietHours.Enabled = true
			c.QuietHours.Start = "00:00"
			c.QuietHours.End = "23:59"
			return &c
		},
		now: func() time.Time { return time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC) },
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Night","sound":true}`))
	app.handleReminderFire(httptest.NewRecorder(), req)
	p := pub.NotifySnapshot()[0]
	if _, ok := p["soundRtttl"]; ok {
		t.Errorf("quiet hours must strip soundRtttl, payload = %v", p)
	}
	if p["name"] != notifyNameReminder {
		t.Errorf("quiet hours must keep the name, got %v", p["name"])
	}
}

// newButtonAckApp returns an app with the Pomodoro engine wired and the button
// callback on, with a hold:true reminder alarm considered live on the clock.
func newButtonAckApp(t *testing.T, pub Publisher) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Pomodoro.Enabled = true
	cfg.Pomodoro.ButtonCallback = true
	cfg.Pomodoro.DBPath = t.TempDir() + "/s.db"
	app := NewApp(cfg, pub, testLogger())
	if err := app.initPomodoro(cfg.Pomodoro); err != nil {
		t.Fatal(err)
	}
	app.reminderHeldUntil.Store(time.Now().Add(time.Minute).UnixNano())
	return app
}

func TestButtonAckDismissesReminderByName(t *testing.T) {
	pub := &recordingPublisher{}
	app := newButtonAckApp(t, pub)

	req := httptest.NewRequest(http.MethodPost, "/hooks/awtrix/button",
		strings.NewReader("button=middle&state=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	app.handleAwtrixButton(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// Blind "dismiss whatever is on screen" is impossible at the type level now:
	// Publisher has no DismissNotify, only DismissNotifyByName.
	if got := pub.DismissedNamesSnapshot(); len(got) != 1 || got[0] != notifyNameReminder {
		t.Fatalf("dismissed names = %v, want [%s]", got, notifyNameReminder)
	}
}

func TestButtonAckToleratesAlreadyDismissedReminder(t *testing.T) {
	pub := &recordingPublisher{dismissByNameErr: &awtrix.APIError{StatusCode: http.StatusNotFound, Code: "notFound"}}
	app := newButtonAckApp(t, pub)

	req := httptest.NewRequest(http.MethodPost, "/hooks/awtrix/button",
		strings.NewReader("button=select&state=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	app.handleAwtrixButton(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if app.reminderHeldUntil.Load() != 0 {
		t.Error("the hold window must be disarmed even when the firmware already cleared the alarm")
	}
}

func TestGenericNotifyIsNamed(t *testing.T) {
	cfg := defaultConfig()
	pub := &recordingPublisher{}
	app := NewApp(cfg, pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/notify", strings.NewReader(`{"text":"hi"}`))
	w := httptest.NewRecorder()
	app.handleNotify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := notifyName(t, pub, 0); got != notifyNameNotify {
		t.Errorf("name = %q, want %q", got, notifyNameNotify)
	}
}

func TestPomodoroPhaseEndAlertIsNamed(t *testing.T) {
	cfg := defaultConfig()
	cfg.Pomodoro.Enabled = true
	cfg.Pomodoro.Sound = true
	pub := &recordingPublisher{}
	app := NewApp(cfg, pub, testLogger())
	app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})
	if got := notifyName(t, pub, 0); got != notifyNamePomodoro {
		t.Errorf("name = %q, want %q", got, notifyNamePomodoro)
	}
}

func TestSunPopupIsNamed(t *testing.T) {
	cfg := defaultConfig()
	pub := &recordingPublisher{}
	app := NewApp(cfg, pub, testLogger())
	now := time.Date(2026, 6, 13, 5, 0, 0, 0, time.UTC)
	app.maybeFireSun(context.Background(), now, now, true, "2026-06-13", cfg.Weather, false, 0)
	if got := notifyName(t, pub, 0); got != notifyNameSunPopup {
		t.Errorf("name = %q, want %q", got, notifyNameSunPopup)
	}
}
