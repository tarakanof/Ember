package render

import (
	"fmt"
	"testing"
	"time"
)

// checkNGKeys fails t with every reason NG 1.1.2 would reject p.
func checkNGKeys(t *testing.T, name string, p map[string]any, notification bool) {
	t.Helper()
	for _, e := range CheckNGPayload(p, notification) {
		t.Errorf("%s: %s", name, e)
	}
}

// TestCheckNGPayloadCatches: the checker itself rejects what NG would.
func TestCheckNGPayloadCatches(t *testing.T) {
	if n := len(ngSharedKeys) + len(ngNotifyOnlyKeys); n != 42 {
		t.Fatalf("key table has %d keys, NG documents 42", n)
	}
	bad := map[string]map[string]any{
		"unknown key":            {"prio": true},
		"notify-only on app":     {"hold": true},
		"bad overlay":            {"overlay": "fog"},
		"bad textCase":           {"textCase": "lower"},
		"bad scroll field":       {"scroll": map[string]any{"smooth": true}},
		"bad scroll enum":        {"scroll": map[string]any{"whenFits": true}},
		"bad scroll mode string": {"scroll": "slide"},
		"repeat not an int":      {"repeat": "1"},
		"negative scroll.speed":  {"scroll": map[string]any{"speed": -1}},
		"textCenter not a bool":  {"textCenter": "false"},
	}
	for name, p := range bad {
		if len(CheckNGPayload(p, false)) == 0 {
			t.Errorf("%s: %v passed the check", name, p)
		}
	}
	good := map[string]any{"hold": true, "soundLoop": true, "repeat": 1,
		"scroll": map[string]any{"mode": "bounce", "whenFits": "static", "holdMs": 0}}
	if errs := CheckNGPayload(good, true); len(errs) != 0 {
		t.Errorf("valid notification rejected: %v", errs)
	}
}

// TestPayloadBuildersEmitOnlyNGKeys guards against a 422: every payload
// builder, in every variant, emits only keys NG 1.1.2 accepts for its route.
func TestPayloadBuildersEmitOnlyNGKeys(t *testing.T) {
	hourly := []float64{3, 4, 5, 6, 7, 8}
	waiting := waitingSnap()
	running := Snapshot{Now: time.Now(), Sessions: []Session{{Source: "mbp", Tool: "claude",
		Session: "s1", State: "running", Activity: "Bash: go test", UpdatedAt: time.Now()}}}
	apps := map[string]map[string]any{
		"weather":         WeatherPayload(WeatherRain, "12°", 12, hourly, 600),
		"weather-moon":    WeatherPayloadMoon("9°", 9, hourly, MoonView{Illum: 0.5, Waxing: true}, 600),
		"weather-native":  WeatherPayloadNative("2422", "12°", 12, hourly, 600),
		"weather-overlay": WithOverlay(WeatherPayload(WeatherStorm, "12°", 12, hourly, 600), OverlayThunder),
		"forecast":        ForecastPayload(hourly, 600),
		"air":             AirPayload(42, hourly, 600),
		"meeting":         MeetingPayload("STANDUP", 12, 600),
		"pomodoro":        PomodoroPayload(PomodoroView{Phase: pomoFocus, RemainingSec: 600, PlannedSec: 1500}, 60),
		"pomodoro-paused": PomodoroPayload(PomodoroView{Phase: pomoFocus, Paused: true, RemainingSec: 600, PlannedSec: 1500}, 60),
		"idle":            RenderIdleFrame(60),
		"attention":       RenderForCoord(waiting, waiting.Sessions[0].Key(), 0, true, 30, nil),
		"source-card":     RenderForCoord(running, running.Sessions[0].Key(), cardSource, false, 30, nil),
		"tool-card":       RenderForCoord(running, running.Sessions[0].Key(), cardTool, false, 30, nil),
	}
	seven := 41
	hot := map[string]*UsageView{"claude": {FiveHourPct: 97, ResetLabel: "17:30", SevenDayPct: &seven,
		Models: []ModelUsage{{Marker: "OP", Pct: 50}, {Marker: "SO", Pct: 20}}}}
	for cursor := 0; cursor < 5; cursor++ {
		apps[fmt.Sprintf("idle-usage-%d", cursor)] = RenderIdleUsagePayload(hot, cursor, time.Now(), 60)
	}
	for name, p := range apps {
		checkNGKeys(t, name, p, false)
	}
	popups := map[string]map[string]any{
		"weather-popup":        WeatherPopupPayload(WeatherRain, "RAIN 12°", "", 30),
		"weather-popup-native": WeatherPopupPayload(WeatherRain, "RAIN 12°", "2422", 30),
		"weather-popup-frost":  WithOverlay(WeatherPopupPayload(WeatherFog, "FOG 1°", "", 30), OverlayFrost),
		"air-popup":            AirPopupPayload(120, 30),
		"sun-popup":            SunPopupPayload(true, "SUNRISE 06:12", 30),
		"limit-reset":          LimitResetPopupPayload("claude", 30),
		"meeting-popup":        MeetingPopupPayload("STANDUP", 2, 30),
		"reminder":             ReminderPopupPayload("Call mom", "", 8, false),
		"reminder-hold":        ReminderPopupPayload("Call mom", "", 8, true),
		"reminder-native":      ReminderPopupPayload("Call mom", "1234", 8, false),
		"notify":               NotifyPayload("Deploy done", "#FFFFFF", "", 5, false),
		"notify-as-typed":      NotifyPayload("Deploy done", "#FFFFFF", "asTyped", 5, false),
	}
	for name, p := range popups {
		checkNGKeys(t, name, p, true)
	}
}
