package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisplayConfigRoundTripAndValidation(t *testing.T) {
	a := newTestAppWithStore(t)

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

	gw := httptest.NewRecorder()
	a.handleDisplayConfigGet(gw, httptest.NewRequest("GET", "/v1/display/config", nil))
	if gw.Code != 200 || !strings.Contains(gw.Body.String(), `"idle_hide_minutes":1`) {
		t.Fatalf("GET = %d body=%s", gw.Code, gw.Body)
	}

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

	pw := httptest.NewRecorder()
	a.handleDisplayConfigPut(pw, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"idle_hide_minutes":3,"attention_hold_seconds":60,"attention_chime":true}`)))
	if pw.Code != 200 {
		t.Fatalf("PUT = %d body=%s", pw.Code, pw.Body)
	}

	if v, ok, _ := a.store.GetSetting(displaySettingsKey); !ok || !strings.Contains(v, `"idle_hide_minutes":3`) {
		t.Fatalf("display settings not persisted: %q ok=%v", v, ok)
	}

	a2 := newTestAppWithStore(t)
	if err := a2.store.PutSetting(displaySettingsKey, `{"idle_hide_minutes":3,"attention_hold_seconds":60,"attention_chime":true}`); err != nil {
		t.Fatal(err)
	}
	a2.settings.reapply()
	cfg2 := a2.cfg.Load()
	if cfg2.Display.IdleRestoreSeconds != 180 || cfg2.Display.AckTimeoutSeconds != 60 || !cfg2.Display.AttentionChime {
		t.Fatalf("persisted settings not applied on load: %+v", cfg2.Display)
	}

	a3 := newTestAppWithStore(t)
	baseline := a3.cfg.Load().Display.AckTimeoutSeconds
	if err := a3.store.PutSetting(displaySettingsKey, `{"idle_hide_minutes":999,"attention_hold_seconds":5}`); err != nil {
		t.Fatal(err)
	}
	a3.settings.reapply()
	if a3.cfg.Load().Display.AckTimeoutSeconds != baseline {
		t.Fatalf("invalid persisted settings should not change baseline; got %d want %d",
			a3.cfg.Load().Display.AckTimeoutSeconds, baseline)
	}
}

func TestDisplayConfigPartialPutKeepsOmittedFields(t *testing.T) {
	a := newTestAppWithStore(t)
	full := httptest.NewRecorder()
	a.handleDisplayConfigPut(full, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"idle_hide_minutes":4,"attention_hold_seconds":45,"attention_chime":true}`)))
	if full.Code != 200 {
		t.Fatalf("full PUT = %d body=%s", full.Code, full.Body)
	}

	pw := httptest.NewRecorder()
	a.handleDisplayConfigPut(pw, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"attention_chime":false}`)))
	if pw.Code != 200 {
		t.Fatalf("partial PUT = %d body=%s", pw.Code, pw.Body)
	}
	d := a.cfg.Load().Display
	if d.IdleRestoreSeconds != 240 || d.AckTimeoutSeconds != 45 || d.AttentionChime {
		t.Fatalf("partial PUT must only flip attention_chime: %+v", d)
	}
}
