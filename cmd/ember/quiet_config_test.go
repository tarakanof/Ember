package main

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"22:00", 22 * 60, true},
		{"08:00", 8 * 60, true},
		{"00:00", 0, true},
		{"23:59", 23*60 + 59, true},
		{"24:00", 0, false},
		{"12:60", 0, false},
		{"aa:bb", 0, false},
		{"", 0, false},
		{"12.30", 0, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := parseHHMM(c.in)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("parseHHMM(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestQuietActive(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 6, 11, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		name       string
		start, end int
		t          time.Time
		want       bool
	}{
		{"overnight inside late", 22 * 60, 8 * 60, at(23, 0), true},
		{"overnight inside early", 22 * 60, 8 * 60, at(3, 0), true},
		{"overnight start boundary", 22 * 60, 8 * 60, at(22, 0), true},
		{"overnight end boundary excluded", 22 * 60, 8 * 60, at(8, 0), false},
		{"overnight outside", 22 * 60, 8 * 60, at(12, 0), false},
		{"normal inside", 13 * 60, 14 * 60, at(13, 30), true},
		{"normal start boundary", 13 * 60, 14 * 60, at(13, 0), true},
		{"normal end boundary excluded", 13 * 60, 14 * 60, at(14, 0), false},
		{"normal outside", 13 * 60, 14 * 60, at(9, 0), false},
		{"degenerate equal never quiet", 9 * 60, 9 * 60, at(9, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quietActive(c.start, c.end, c.t); got != c.want {
				t.Errorf("quietActive(%d,%d,%v) = %v want %v", c.start, c.end, c.t, got, c.want)
			}
		})
	}
}

func TestQuietHoursWindowDefaults(t *testing.T) {
	t.Run("zero config defaults to 22:00-08:00 disabled", func(t *testing.T) {
		var c Config
		enabled, start, end := c.quietHoursWindow()
		if enabled || start != 22*60 || end != 8*60 {
			t.Errorf("zero config = %v,%d,%d want false,1320,480", enabled, start, end)
		}
	})

	t.Run("explicit config returned as-is", func(t *testing.T) {
		c := Config{QuietHours: QuietHoursConfig{Enabled: true, Start: "23:30", End: "07:15"}}
		enabled, start, end := c.quietHoursWindow()
		if !enabled || start != 23*60+30 || end != 7*60+15 {
			t.Errorf("explicit config = %v,%d,%d want true,1410,435", enabled, start, end)
		}
	})
}

func TestValidateQuietHours(t *testing.T) {
	t.Run("empty fields valid (defaults apply)", func(t *testing.T) {
		if err := validateQuietHours(QuietHoursConfig{}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid explicit window accepted", func(t *testing.T) {
		if err := validateQuietHours(QuietHoursConfig{Start: "22:00", End: "08:00"}); err != nil {
			t.Errorf("valid window rejected: %v", err)
		}
	})

	t.Run("bad start rejected with sentinel", func(t *testing.T) {
		err := validateQuietHours(QuietHoursConfig{Start: "25:00"})
		if err == nil {
			t.Fatal("bad start accepted")
		}
		if !errors.Is(err, ErrConfigValidate) {
			t.Errorf("error not wrapped with ErrConfigValidate: %v", err)
		}
	})

	t.Run("bad end rejected with sentinel", func(t *testing.T) {
		err := validateQuietHours(QuietHoursConfig{End: "nope"})
		if err == nil {
			t.Fatal("bad end accepted")
		}
		if !errors.Is(err, ErrConfigValidate) {
			t.Errorf("error not wrapped with ErrConfigValidate: %v", err)
		}
	})
}

func TestQuietConfigDTOValidate(t *testing.T) {
	t.Run("good DTO accepted", func(t *testing.T) {
		good := quietConfigDTO{Enabled: true, Start: "22:00", End: "08:00"}
		if err := good.validate(); err != nil {
			t.Errorf("good DTO rejected: %v", err)
		}
	})

	for _, bad := range []struct {
		name string
		dto  quietConfigDTO
	}{
		{"bad start", quietConfigDTO{Start: "24:00", End: "08:00"}},
		{"empty end", quietConfigDTO{Start: "22:00", End: ""}},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if err := bad.dto.validate(); err == nil {
				t.Errorf("bad DTO %+v accepted", bad.dto)
			}
		})
	}
}

func TestQuietDTODefaultsAndApply(t *testing.T) {
	a := newTestAppWithStore(t)

	t.Run("default DTO shows disabled with 22:00-08:00 window", func(t *testing.T) {
		d := a.quietDTO()
		if d.Enabled || d.Start != "22:00" || d.End != "08:00" {
			t.Fatalf("default DTO = %+v, want disabled 22:00-08:00", d)
		}
	})

	t.Run("applyQuietSettings updates live config", func(t *testing.T) {
		a.applyQuietSettings(quietConfigDTO{Enabled: true, Start: "23:00", End: "07:00"})
		q := a.cfg.Load().QuietHours
		if !q.Enabled || q.Start != "23:00" || q.End != "07:00" {
			t.Fatalf("applied config = %+v", q)
		}
	})
}

func TestQuietConfigRoundTripAndValidation(t *testing.T) {
	a := newTestAppWithStore(t)

	// GET returns defaults: disabled, 22:00-08:00.
	gw := httptest.NewRecorder()
	a.handleQuietConfigGet(gw, httptest.NewRequest("GET", "/v1/quiet/config", nil))
	if gw.Code != 200 || !strings.Contains(gw.Body.String(), `"start":"22:00"`) {
		t.Fatalf("GET default = %d body=%s", gw.Code, gw.Body)
	}

	// PUT valid → effective values change.
	pw := httptest.NewRecorder()
	a.handleQuietConfigPut(pw, httptest.NewRequest("PUT", "/v1/quiet/config",
		strings.NewReader(`{"enabled":true,"start":"23:00","end":"07:00"}`)))
	if pw.Code != 200 {
		t.Fatalf("PUT = %d body=%s", pw.Code, pw.Body)
	}
	if !strings.Contains(pw.Body.String(), `"start":"23:00"`) || !strings.Contains(pw.Body.String(), `"end":"07:00"`) {
		t.Fatalf("PUT response missing new values: %s", pw.Body)
	}
	q := a.cfg.Load().QuietHours
	if !q.Enabled || q.Start != "23:00" || q.End != "07:00" {
		t.Fatalf("config not applied: %+v", q)
	}

	// PUT with bad body (invalid start) → 400.
	bw := httptest.NewRecorder()
	a.handleQuietConfigPut(bw, httptest.NewRequest("PUT", "/v1/quiet/config",
		strings.NewReader(`{"start":"25:00"}`)))
	if bw.Code != 400 {
		t.Fatalf("expected 400 for bad start, got %d body=%s", bw.Code, bw.Body)
	}

	// Partial PUT: only change enabled → start stays "23:00" (pre-seed guard).
	ppw := httptest.NewRecorder()
	a.handleQuietConfigPut(ppw, httptest.NewRequest("PUT", "/v1/quiet/config",
		strings.NewReader(`{"enabled":false}`)))
	if ppw.Code != 200 {
		t.Fatalf("partial PUT = %d body=%s", ppw.Code, ppw.Body)
	}
	if !strings.Contains(ppw.Body.String(), `"start":"23:00"`) {
		t.Fatalf("partial PUT should preserve start=23:00; got: %s", ppw.Body)
	}
	if a.cfg.Load().QuietHours.Enabled {
		t.Fatalf("partial PUT should have set enabled=false")
	}
}

func TestQuietConfigPersistence(t *testing.T) {
	a := newTestAppWithStore(t)

	// PUT a valid config to persist it.
	pw := httptest.NewRecorder()
	a.handleQuietConfigPut(pw, httptest.NewRequest("PUT", "/v1/quiet/config",
		strings.NewReader(`{"enabled":true,"start":"23:00","end":"07:00"}`)))
	if pw.Code != 200 {
		t.Fatalf("PUT = %d body=%s", pw.Code, pw.Body)
	}

	// Confirm the blob is actually stored under the right key.
	if v, ok, _ := a.store.GetSetting(quietSettingsKey); !ok || !strings.Contains(v, `"start":"23:00"`) {
		t.Fatalf("quiet settings not persisted: %q ok=%v", v, ok)
	}

	// Simulate restart: new App over a fresh store, inject stored value, loadPersistedQuietSettings.
	a2 := newTestAppWithStore(t)
	if err := a2.store.PutSetting(quietSettingsKey, `{"enabled":true,"start":"23:00","end":"07:00"}`); err != nil {
		t.Fatal(err)
	}
	a2.loadPersistedQuietSettings()
	cfg2 := a2.cfg.Load()
	if !cfg2.QuietHours.Enabled || cfg2.QuietHours.Start != "23:00" || cfg2.QuietHours.End != "07:00" {
		t.Fatalf("persisted settings not applied on load: %+v", cfg2.QuietHours)
	}

	// Simulate restart with legacy blob missing a field — pre-seed guard keeps default.
	// A blob without "end" should keep the live-config default ("08:00"), not zero it.
	a3 := newTestAppWithStore(t)
	if err := a3.store.PutSetting(quietSettingsKey, `{"enabled":true,"start":"23:00"}`); err != nil {
		t.Fatal(err)
	}
	a3.loadPersistedQuietSettings()
	cfg3 := a3.cfg.Load()
	if cfg3.QuietHours.End != "08:00" {
		t.Fatalf("legacy blob missing 'end' should keep default 08:00, got %q", cfg3.QuietHours.End)
	}

	// Corrupt/invalid stored blob → ignored, baseline kept.
	a4 := newTestAppWithStore(t)
	baseline := a4.cfg.Load().QuietHours.Start
	if err := a4.store.PutSetting(quietSettingsKey, `{"enabled":true,"start":"25:00","end":"07:00"}`); err != nil {
		t.Fatal(err)
	}
	a4.loadPersistedQuietSettings()
	if a4.cfg.Load().QuietHours.Start != baseline {
		t.Fatalf("invalid persisted settings should not change baseline; got %q want %q",
			a4.cfg.Load().QuietHours.Start, baseline)
	}
}
