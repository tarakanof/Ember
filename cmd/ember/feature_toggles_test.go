package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPomodoroEnableToggleRuntime(t *testing.T) {
	a := newTestAppWithStore(t)
	// Always-wire the engine; default config has enabled=false.
	if err := a.initPomodoro(a.cfg.Load().Pomodoro); err != nil {
		t.Fatal(err)
	}
	if a.pomodoroOn() {
		t.Fatal("should start disabled (default config enabled=false)")
	}

	// config GET is available even when disabled, and reports enabled:false.
	gw := httptest.NewRecorder()
	a.handlePomodoroConfigGet(gw, httptest.NewRequest("GET", "/v1/pomodoro/config", nil))
	if gw.Code != http.StatusOK || !strings.Contains(gw.Body.String(), `"enabled":false`) {
		t.Fatalf("config GET while disabled: code=%d body=%s", gw.Code, gw.Body.String())
	}

	// start is 404 while disabled.
	sw := httptest.NewRecorder()
	a.handlePomodoroStart(sw, httptest.NewRequest("POST", "/v1/pomodoro/start", nil))
	if sw.Code != http.StatusNotFound {
		t.Fatalf("start while disabled: code=%d want 404", sw.Code)
	}

	// Enable via PUT (flip enabled on the current valid DTO).
	var dto pomodoroSettingsDTO
	if err := json.Unmarshal(gw.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	on := true
	dto.Enabled = &on
	body, _ := json.Marshal(dto)
	pw := httptest.NewRecorder()
	a.handlePomodoroConfigPut(pw, httptest.NewRequest("PUT", "/v1/pomodoro/config", strings.NewReader(string(body))))
	if pw.Code != http.StatusOK || !strings.Contains(pw.Body.String(), `"enabled":true`) {
		t.Fatalf("enable PUT: code=%d body=%s", pw.Code, pw.Body.String())
	}
	if !a.pomodoroOn() {
		t.Fatal("pomodoroOn should be true after enable")
	}
	// persisted (so it survives restart)
	if v, ok, _ := a.store.GetSetting(pomodoroSettingsKey); !ok || !strings.Contains(v, `"enabled":true`) {
		t.Fatalf("enabled not persisted: %q ok=%v", v, ok)
	}

	// start now works.
	sw2 := httptest.NewRecorder()
	a.handlePomodoroStart(sw2, httptest.NewRequest("POST", "/v1/pomodoro/start", nil))
	if sw2.Code == http.StatusNotFound {
		t.Fatalf("start after enable still 404")
	}

	// Disable again → start 404 again.
	off := false
	dto.Enabled = &off
	body, _ = json.Marshal(dto)
	dw := httptest.NewRecorder()
	a.handlePomodoroConfigPut(dw, httptest.NewRequest("PUT", "/v1/pomodoro/config", strings.NewReader(string(body))))
	if dw.Code != http.StatusOK || a.pomodoroOn() {
		t.Fatalf("disable PUT: code=%d on=%v", dw.Code, a.pomodoroOn())
	}
}

func TestUsageConfigToggles(t *testing.T) {
	a := newTestAppWithStore(t)
	if !a.cfg.Load().usageWidgetEnabled() {
		t.Fatal("usage widget should default on")
	}
	pw := httptest.NewRecorder()
	a.handleUsageConfigPut(pw, httptest.NewRequest("PUT", "/v1/usage/config",
		strings.NewReader(`{"usage_widget":false,"usage_per_model":true}`)))
	if pw.Code != http.StatusOK {
		t.Fatalf("put: code=%d body=%s", pw.Code, pw.Body.String())
	}
	if a.cfg.Load().usageWidgetEnabled() {
		t.Fatal("usage widget should be off after PUT")
	}
	if !a.cfg.Load().usagePerModelEnabled() {
		t.Fatal("per-model should be on after PUT")
	}
	if v, ok, _ := a.store.GetSetting(usageSettingsKey); !ok || !strings.Contains(v, `"usage_widget":false`) {
		t.Fatalf("usage not persisted: %q ok=%v", v, ok)
	}
	gw := httptest.NewRecorder()
	a.handleUsageConfigGet(gw, httptest.NewRequest("GET", "/v1/usage/config", nil))
	if !strings.Contains(gw.Body.String(), `"usage_widget":false`) {
		t.Fatalf("get: %s", gw.Body.String())
	}
}
