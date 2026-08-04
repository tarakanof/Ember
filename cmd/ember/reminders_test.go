package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if p["soundRtttl"] != defaultReminderSound {
		t.Errorf("soundRtttl = %v, want %q", p["soundRtttl"], defaultReminderSound)
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

func TestHandleReminderFireValidation(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	w := httptest.NewRecorder()
	app.handleReminderFire(w, httptest.NewRequest(http.MethodPost, "/v1/reminders/fire",
		strings.NewReader(`{"text":"  "}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("blank text should 400, got %d", w.Code)
	}
}
