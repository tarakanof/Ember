package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleReminderFireRendersBellPopup(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Stand-up","sound":true,"duration":10,"hold":true}`))
	w := httptest.NewRecorder()
	app.handleReminderFire(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.notify) != 1 {
		t.Fatalf("expected 1 popup, got %d", len(pub.notify))
	}
	p := pub.notify[0]
	if p["text"] != "Stand-up" {
		t.Errorf("text = %v, want Stand-up", p["text"])
	}
	if p["durationMs"] != 10_000 {
		t.Errorf("durationMs = %v, want 10000", p["durationMs"])
	}
	// A held alarm rings until dismissed: the looped melody (with a trailing
	// rest between rings) plus NG's soundLoop.
	if p["soundRtttl"] != defaultReminderAlarm {
		t.Errorf("soundRtttl = %v, want %q", p["soundRtttl"], defaultReminderAlarm)
	}
	if p["soundLoop"] != true {
		t.Errorf("soundLoop = %v, want true on a held alarm", p["soundLoop"])
	}
	if p["hold"] != true {
		t.Errorf("hold = %v, want true", p["hold"])
	}
	if _, hasDraw := p["draw"]; !hasDraw {
		t.Error("no native_icon_id -> should draw the bell")
	}
	// The chime rides on the notification; nothing is played out-of-band.
	if len(pub.rtttls) != 0 {
		t.Errorf("expected no out-of-band chime, got %v", pub.rtttls)
	}
}

func TestHandleReminderFireNativeIconAndSilent(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Walk","sound":false,"duration":8,"native_icon_id":"1234"}`))
	app.handleReminderFire(httptest.NewRecorder(), req)
	pub.mu.Lock()
	defer pub.mu.Unlock()
	p := pub.notify[0]
	if p["icon"] != "1234" {
		t.Errorf("icon = %v, want 1234", p["icon"])
	}
	if _, hasSound := p["soundRtttl"]; hasSound {
		t.Error("sound=false should omit soundRtttl")
	}
	if len(pub.rtttls) != 0 {
		t.Errorf("sound=false should play no chime, got %v", pub.rtttls)
	}
}

// TestHandleReminderFireUnheldChimesOnce: a reminder that auto-dismisses plays
// its chime once; only a held alarm loops.
func TestHandleReminderFireUnheldChimesOnce(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	app.handleReminderFire(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/v1/reminders/fire", strings.NewReader(`{"text":"Walk","sound":true,"duration":8}`)))
	p := pub.NotifySnapshot()[0]
	if p["soundRtttl"] != defaultReminderSound {
		t.Errorf("soundRtttl = %v, want %q", p["soundRtttl"], defaultReminderSound)
	}
	if _, has := p["soundLoop"]; has {
		t.Errorf("an unheld reminder must not loop its chime, payload = %v", p)
	}
}

// TestHandleReminderFireSilentHoldDoesNotLoop: soundLoop without a melody is
// pointless; a silent held alarm carries neither.
func TestHandleReminderFireSilentHoldDoesNotLoop(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	app.handleReminderFire(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/v1/reminders/fire", strings.NewReader(`{"text":"Walk","sound":false,"hold":true}`)))
	p := pub.NotifySnapshot()[0]
	for _, k := range []string{"soundRtttl", "soundLoop"} {
		if _, has := p[k]; has {
			t.Errorf("silent held alarm carries %q, payload = %v", k, p)
		}
	}
}

func TestHandleReminderFireValidation(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	w := httptest.NewRecorder()
	app.handleReminderFire(w, httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"  "}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("blank text should 400, got %d", w.Code)
	}
}

func fireWithKey(t *testing.T, app *App, key string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"Stand-up","duration":8}`))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	app.handleReminderFire(w, req)
	return w.Code
}

func TestHandleReminderFireDuplicateKeyDoesNotRingAgain(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	if code := fireWithKey(t, app, "r1|100"); code != http.StatusNoContent {
		t.Fatalf("first fire status = %d, want 204", code)
	}
	if code := fireWithKey(t, app, "r1|100"); code != http.StatusOK {
		t.Fatalf("duplicate fire status = %d, want 200", code)
	}
	if n := len(pub.NotifySnapshot()); n != 1 {
		t.Fatalf("popups = %d, want 1", n)
	}
}

func TestHandleReminderFireDistinctOrMissingKeysAllRing(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	for _, key := range []string{"r1|100", "r1|160", "", ""} {
		if code := fireWithKey(t, app, key); code != http.StatusNoContent {
			t.Fatalf("fire %q status = %d, want 204", key, code)
		}
	}
	if n := len(pub.NotifySnapshot()); n != 4 {
		t.Fatalf("popups = %d, want 4", n)
	}
}

func TestHandleReminderFireFailedPushReleasesKey(t *testing.T) {
	pub := &recordingPublisher{}
	failed := false
	pub.failNotify = func() error {
		if !failed {
			failed = true
			return errors.New("clock unreachable")
		}
		return nil
	}
	app := NewApp(defaultConfig(), pub, testLogger())
	if code := fireWithKey(t, app, "r1|100"); code != http.StatusBadGateway {
		t.Fatalf("failed fire status = %d, want 502", code)
	}
	if code := fireWithKey(t, app, "r1|100"); code != http.StatusNoContent {
		t.Fatalf("retry after failure status = %d, want 204", code)
	}
	if n := len(pub.NotifySnapshot()); n != 1 {
		t.Fatalf("popups = %d, want 1", n)
	}
}

func TestReminderDedupeForgetsKeysAfterTTL(t *testing.T) {
	var d reminderDedupe
	now := time.Unix(1_000_000, 0)
	if !d.claim("k", now) {
		t.Fatal("first claim should succeed")
	}
	if d.claim("k", now.Add(reminderDedupeTTL-time.Second)) {
		t.Fatal("claim inside the TTL should be refused")
	}
	if !d.claim("k", now.Add(reminderDedupeTTL+time.Second)) {
		t.Fatal("claim after the TTL should succeed")
	}
	d.claim("other", now.Add(3*reminderDedupeTTL))
	if n := d.size(); n != 1 {
		t.Fatalf("expired keys should be pruned, size = %d, want 1", n)
	}
}
