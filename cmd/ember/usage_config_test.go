package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageThresholdDefaultAndClamp(t *testing.T) {
	var c Config
	if got := c.usageThresholdPct(); got != 60 {
		t.Fatalf("nil threshold: got %d, want default 60", got)
	}
	for in, want := range map[int]int{-5: 0, 0: 0, 60: 60, 150: 100} {
		v := in
		c.UsageThresholdPct = &v
		if got := c.usageThresholdPct(); got != want {
			t.Errorf("threshold %d: got %d, want %d", in, got, want)
		}
	}
}

func TestUsageConfigThresholdRoundtrip(t *testing.T) {
	a := newTestAppWithStore(t)

	// GET default includes the threshold.
	gw := httptest.NewRecorder()
	a.handleUsageConfigGet(gw, httptest.NewRequest("GET", "/v1/usage/config", nil))
	if gw.Code != http.StatusOK || !strings.Contains(gw.Body.String(), `"usage_threshold_pct":60`) {
		t.Fatalf("GET default: code=%d body=%s", gw.Code, gw.Body.String())
	}

	// PUT a new threshold; other fields keep their values (pre-seed guard).
	pw := httptest.NewRecorder()
	pr := httptest.NewRequest("PUT", "/v1/usage/config", strings.NewReader(`{"usage_threshold_pct":75}`))
	a.handleUsageConfigPut(pw, pr)
	if pw.Code != http.StatusOK || !strings.Contains(pw.Body.String(), `"usage_threshold_pct":75`) {
		t.Fatalf("PUT: code=%d body=%s", pw.Code, pw.Body.String())
	}
	if !strings.Contains(pw.Body.String(), `"usage_widget":true`) {
		t.Fatalf("PUT zeroed unrelated field: %s", pw.Body.String())
	}
	if got := a.cfg.Load().usageThresholdPct(); got != 75 {
		t.Fatalf("live config: got %d, want 75", got)
	}
	// Persisted for restart.
	if v, ok, _ := a.store.GetSetting(usageSettingsKey); !ok || !strings.Contains(v, `"usage_threshold_pct":75`) {
		t.Fatalf("threshold not persisted: %q ok=%v", v, ok)
	}

	// An out-of-range value is rejected like every other settings PUT (it
	// used to be clamped silently); the live and persisted value stay 75.
	for _, bad := range []string{`{"usage_threshold_pct":150}`, `{"usage_threshold_pct":-1}`} {
		pw2 := httptest.NewRecorder()
		a.handleUsageConfigPut(pw2, httptest.NewRequest("PUT", "/v1/usage/config", strings.NewReader(bad)))
		if pw2.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s: code=%d body=%s, want 400", bad, pw2.Code, pw2.Body.String())
		}
	}
	if got := a.cfg.Load().usageThresholdPct(); got != 75 {
		t.Fatalf("rejected PUT changed the live threshold to %d", got)
	}
	if v, ok, _ := a.store.GetSetting(usageSettingsKey); !ok || !strings.Contains(v, `"usage_threshold_pct":75`) {
		t.Fatalf("rejected PUT changed the persisted blob: %q ok=%v", v, ok)
	}
}
