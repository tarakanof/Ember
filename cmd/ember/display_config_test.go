package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisplayConfigRoundTripAndValidation(t *testing.T) {
	a := newTestAppWithStore(t)

	// PUT valid → effective values change.
	pw := httptest.NewRecorder()
	a.handleDisplayConfigPut(pw, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"idle_hide_minutes":1,"attention_hold_seconds":45,"attention_chime":true}`)))
	if pw.Code != 200 {
		t.Fatalf("PUT = %d body=%s", pw.Code, pw.Body)
	}
	cfg := a.cfg.Load()
	if cfg.Display.IdleRestoreSeconds != 60 || cfg.Display.AckTimeoutSeconds != 45 || !cfg.Display.AttentionChime {
		t.Fatalf("config not applied: %+v", cfg.Display)
	}

	// GET returns effective values.
	gw := httptest.NewRecorder()
	a.handleDisplayConfigGet(gw, httptest.NewRequest("GET", "/v1/display/config", nil))
	if gw.Code != 200 || !strings.Contains(gw.Body.String(), `"idle_hide_minutes":1`) {
		t.Fatalf("GET = %d body=%s", gw.Code, gw.Body)
	}

	// Out-of-range rejected (idle 0-60, hold 5-300).
	for _, bad := range []string{
		`{"idle_hide_minutes":99,"attention_hold_seconds":45}`,
		`{"idle_hide_minutes":-1,"attention_hold_seconds":45}`,
		`{"idle_hide_minutes":2,"attention_hold_seconds":4}`,
		`{"idle_hide_minutes":2,"attention_hold_seconds":301}`,
	} {
		bw := httptest.NewRecorder()
		a.handleDisplayConfigPut(bw, httptest.NewRequest("PUT", "/v1/display/config", strings.NewReader(bad)))
		if bw.Code != 400 {
			t.Fatalf("expected 400 for %s, got %d", bad, bw.Code)
		}
	}
}

func TestDisplayConfigPersistence(t *testing.T) {
	a := newTestAppWithStore(t)

	// PUT a valid config to persist it.
	pw := httptest.NewRecorder()
	a.handleDisplayConfigPut(pw, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"idle_hide_minutes":3,"attention_hold_seconds":60,"attention_chime":true}`)))
	if pw.Code != 200 {
		t.Fatalf("PUT = %d body=%s", pw.Code, pw.Body)
	}

	// Confirm the blob is actually stored.
	if v, ok, _ := a.store.GetSetting(displaySettingsKey); !ok || !strings.Contains(v, `"idle_hide_minutes":3`) {
		t.Fatalf("display settings not persisted: %q ok=%v", v, ok)
	}

	// Simulate restart: new App over the same store, then loadPersistedDisplaySettings.
	a2 := newTestAppWithStore(t)
	// Manually inject stored value to a2's store so we can test the load.
	if err := a2.store.PutSetting(displaySettingsKey, `{"idle_hide_minutes":3,"attention_hold_seconds":60,"attention_chime":true}`); err != nil {
		t.Fatal(err)
	}
	a2.loadPersistedDisplaySettings()
	cfg2 := a2.cfg.Load()
	if cfg2.Display.IdleRestoreSeconds != 180 || cfg2.Display.AckTimeoutSeconds != 60 || !cfg2.Display.AttentionChime {
		t.Fatalf("persisted settings not applied on load: %+v", cfg2.Display)
	}

	// Invalid stored blob → ignored, baseline kept.
	a3 := newTestAppWithStore(t)
	baseline := a3.cfg.Load().Display.AckTimeoutSeconds
	if err := a3.store.PutSetting(displaySettingsKey, `{"idle_hide_minutes":999,"attention_hold_seconds":5}`); err != nil {
		t.Fatal(err)
	}
	a3.loadPersistedDisplaySettings()
	if a3.cfg.Load().Display.AckTimeoutSeconds != baseline {
		t.Fatalf("invalid persisted settings should not change baseline; got %d want %d",
			a3.cfg.Load().Display.AckTimeoutSeconds, baseline)
	}
}
