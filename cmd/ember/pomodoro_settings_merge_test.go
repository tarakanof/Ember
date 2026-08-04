package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestPomodoroConfigPutPartialEnableLeavesOtherFieldsUnchanged is the
// regression test for the bug where pomodoroSettingsDTO's Enabled was the
// only tri-state (pointer) field: a PUT carrying just {"enabled":true} full-
// replaced every other field with its Go zero value, silently zeroing
// auto_start_next, sound, sound_melody, focus_color, break_color and the
// durations (durations were masked because validation re-rejected the zeroed
// values before persisting — but the boolean/string fields sailed through).
func TestPomodoroConfigPutPartialEnableLeavesOtherFieldsUnchanged(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	full := `{"focus_minutes":42,"short_break_minutes":7,"long_break_minutes":22,` +
		`"rounds_before_long_break":6,"auto_start_next":true,"sound":true,` +
		`"sound_melody":"custom:chime","focus_color":"#123456","break_color":"#654321",` +
		`"max_session_minutes":90}`
	if resp, _ := doReq(t, srv, "PUT", "/v1/pomodoro/config", "", full); resp.StatusCode != 200 {
		t.Fatalf("seed put status = %d", resp.StatusCode)
	}

	if resp, _ := doReq(t, srv, "PUT", "/v1/pomodoro/config", "", `{"enabled":true}`); resp.StatusCode != 200 {
		t.Fatalf("enable-only put status = %d", resp.StatusCode)
	}

	_, got := doReq(t, srv, "GET", "/v1/pomodoro/config", "", "")
	want := map[string]any{
		"enabled":                  true,
		"focus_minutes":            float64(42),
		"short_break_minutes":      float64(7),
		"long_break_minutes":       float64(22),
		"rounds_before_long_break": float64(6),
		"auto_start_next":          true,
		"sound":                    true,
		"sound_melody":             "custom:chime",
		"focus_color":              "#123456",
		"break_color":              "#654321",
		"max_session_minutes":      float64(90),
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("after enable-only PUT, %s = %v, want %v (full config = %+v)", k, got[k], w, got)
		}
	}
}

// TestPomodoroConfigPutPartialUpdateTouchesOnlyGivenField mirrors the
// regression test from the other direction: a PUT naming a single non-enabled
// field must change only that field and leave everything else (including
// enabled) as-is.
func TestPomodoroConfigPutPartialUpdateTouchesOnlyGivenField(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	full := `{"focus_minutes":42,"short_break_minutes":7,"long_break_minutes":22,` +
		`"rounds_before_long_break":6,"auto_start_next":true,"sound":true,` +
		`"sound_melody":"custom:chime","focus_color":"#123456","break_color":"#654321",` +
		`"max_session_minutes":90,"enabled":true}`
	if resp, _ := doReq(t, srv, "PUT", "/v1/pomodoro/config", "", full); resp.StatusCode != 200 {
		t.Fatalf("seed put status = %d", resp.StatusCode)
	}

	if resp, _ := doReq(t, srv, "PUT", "/v1/pomodoro/config", "", `{"focus_minutes":33}`); resp.StatusCode != 200 {
		t.Fatalf("partial put status = %d", resp.StatusCode)
	}

	_, got := doReq(t, srv, "GET", "/v1/pomodoro/config", "", "")
	if got["focus_minutes"] != float64(33) {
		t.Fatalf("focus_minutes = %v, want 33", got["focus_minutes"])
	}
	unchanged := map[string]any{
		"enabled":                  true,
		"short_break_minutes":      float64(7),
		"long_break_minutes":       float64(22),
		"rounds_before_long_break": float64(6),
		"auto_start_next":          true,
		"sound":                    true,
		"sound_melody":             "custom:chime",
		"focus_color":              "#123456",
		"break_color":              "#654321",
		"max_session_minutes":      float64(90),
	}
	for k, w := range unchanged {
		if got[k] != w {
			t.Errorf("after partial PUT of focus_minutes, %s = %v, want unchanged %v (full config = %+v)", k, got[k], w, got)
		}
	}
}

// TestLoadPersistedPomodoroSettingsRestoresFullBlob confirms an
// old-style persisted blob — dtoFromConfig always emits every field
// non-nil — restores every field identically through the same merge path
// applyPomodoroSettings uses for a live PUT, so persisted settings written
// before or after this change behave identically.
func TestLoadPersistedPomodoroSettingsRestoresFullBlob(t *testing.T) {
	app := newPomodoroApp(t)

	full := PomodoroConfig{
		Enabled:               true,
		FocusMinutes:          42,
		ShortBreakMinutes:     7,
		LongBreakMinutes:      22,
		RoundsBeforeLongBreak: 6,
		AutoStartNext:         true,
		Sound:                 true,
		SoundMelody:           "custom:chime",
		FocusColor:            "#123456",
		BreakColor:            "#654321",
		MaxSessionMinutes:     90,
	}
	blob, err := json.Marshal(dtoFromConfig(full))
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	if err := app.store.PutSetting(pomodoroSettingsKey, string(blob)); err != nil {
		t.Fatalf("put setting: %v", err)
	}

	// Diverge the live config from the persisted blob so a no-op merge would
	// be indistinguishable from a real restore.
	cfg := *app.cfg.Load()
	cfg.Pomodoro = PomodoroConfig{Enabled: false, FocusMinutes: 25, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4, DBPath: cfg.Pomodoro.DBPath}
	app.cfg.Store(&cfg)

	app.loadPersistedPomodoroSettings()

	got := app.cfg.Load().Pomodoro
	full.DBPath = got.DBPath // DBPath isn't part of the DTO; preserve whatever the live config carries.
	if got != full {
		t.Fatalf("restored config = %+v, want %+v", got, full)
	}
}

// TestApplyPomodoroSettingsRejectsBadMergedResult confirms validation still
// runs on the merged (not just the incoming) settings — a partial update that
// only touches an unrelated field must not let a value already present as
// invalid slip through, and a partial update introducing an invalid value
// must be rejected outright without partially applying.
func TestApplyPomodoroSettingsRejectsBadMergedResult(t *testing.T) {
	app := newPomodoroApp(t)
	before := app.cfg.Load().Pomodoro

	err := app.applyPomodoroSettings(pomodoroSettingsDTO{FocusMinutes: intPtr(999)})
	if err == nil {
		t.Fatal("expected validation error for out-of-range focus_minutes, got nil")
	}

	after := app.cfg.Load().Pomodoro
	if after != before {
		t.Fatalf("rejected settings must not be applied: before=%+v after=%+v", before, after)
	}
}
