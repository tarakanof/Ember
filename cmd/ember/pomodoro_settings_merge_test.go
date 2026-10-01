package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestPomodoroConfigPutPartialEnableLeavesOtherFieldsUnchanged(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	full := `{"focus_minutes":42,"short_break_minutes":7,"long_break_minutes":22,` +
		`"rounds_before_long_break":6,"auto_start_next":true,"sound":true,` +
		`"sound_melody":"custom:chime","focus_color":"#123456","break_color":"#654321",` +
		`"max_session_minutes":90,"daily_goal_sessions":6,"weekly_goal_days":3}`
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
		"daily_goal_sessions":      float64(6),
		"weekly_goal_days":         float64(3),
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("after enable-only PUT, %s = %v, want %v (full config = %+v)", k, got[k], w, got)
		}
	}
}

func TestPomodoroConfigPutPartialUpdateTouchesOnlyGivenField(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	full := `{"focus_minutes":42,"short_break_minutes":7,"long_break_minutes":22,` +
		`"rounds_before_long_break":6,"auto_start_next":true,"sound":true,` +
		`"sound_melody":"custom:chime","focus_color":"#123456","break_color":"#654321",` +
		`"max_session_minutes":90,"daily_goal_sessions":6,"weekly_goal_days":3,"enabled":true}`
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
		"daily_goal_sessions":      float64(6),
		"weekly_goal_days":         float64(3),
	}
	for k, w := range unchanged {
		if got[k] != w {
			t.Errorf("after partial PUT of focus_minutes, %s = %v, want unchanged %v (full config = %+v)", k, got[k], w, got)
		}
	}
}

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
		DailyGoalSessions:     6,
		WeeklyGoalDays:        3,
	}
	blob, err := json.Marshal(dtoFromConfig(full))
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	if err := app.store.PutSetting(pomodoroSettingsKey, string(blob)); err != nil {
		t.Fatalf("put setting: %v", err)
	}

	cfg := *app.cfg.Load()
	cfg.Pomodoro = PomodoroConfig{Enabled: false, FocusMinutes: 25, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4, DBPath: cfg.Pomodoro.DBPath}
	app.cfg.Store(&cfg)

	app.settings.reapply()

	got := app.cfg.Load().Pomodoro
	full.DBPath = got.DBPath
	if got != full {
		t.Fatalf("restored config = %+v, want %+v", got, full)
	}
}

func TestApplyPomodoroSettingsRejectsBadMergedResult(t *testing.T) {
	app := newPomodoroApp(t)
	before := app.cfg.Load().Pomodoro

	_, err := app.settings.pomodoro.put([]byte(`{"focus_minutes":999}`))
	if err == nil {
		t.Fatal("expected validation error for out-of-range focus_minutes, got nil")
	}

	after := app.cfg.Load().Pomodoro
	if after != before {
		t.Fatalf("rejected settings must not be applied: before=%+v after=%+v", before, after)
	}
}
