package render

import (
	"testing"
	"time"
)

// ngSharedKeys are the 35 top-level payload keys awtrix-ng 1.1.2 accepts on
// both pushed apps and notifications, and ngNotifyOnlyKeys the 7 it accepts on
// notifications only (reference/payload: "exactly 42 top-level keys"). Any
// other key makes NG reject the whole push with 422, so every builder is held
// to this list.
var ngSharedKeys = map[string]bool{
	"text": true, "textCase": true, "font": true, "textColor": true,
	"textBlinkMs": true, "textFadeMs": true, "textCenter": true, "scroll": true,
	"textOffsetX": true, "textInFront": true,
	"icon": true, "iconMode": true, "iconOffsetX": true, "iconGap": true, "icons": true,
	"durationMs": true, "lifetimeMs": true, "lifetimeExpiry": true, "repeat": true,
	"backgroundColor": true, "overlay": true, "draw": true,
	"barChart": true, "lineChart": true, "chartAutoscale": true, "chartColor": true,
	"progress": true, "progressColor": true, "progressTrackColor": true,
	"effect": true, "effectSpeed": true,
	"palette": true, "paletteBlend": true, "paletteSpan": true, "paletteSpeed": true,
}

var ngNotifyOnlyKeys = map[string]bool{
	"name": true, "hold": true, "stack": true, "wakeup": true,
	"sound": true, "soundRtttl": true, "soundLoop": true,
}

// ngScrollKeys are the seven fields of the scroll object.
var ngScrollKeys = map[string]bool{
	"mode": true, "direction": true, "entry": true, "whenFits": true,
	"speed": true, "gap": true, "holdMs": true,
}

// ngOverlays and ngTextCases are NG's enum values for overlay and textCase;
// a word outside the list is a 422 like an unknown key.
var (
	ngOverlays  = map[string]bool{"rain": true, "snow": true, "drizzle": true, "storm": true, "thunder": true, "frost": true}
	ngTextCases = map[string]bool{"inherit": true, "upper": true, "asTyped": true}
)

// checkNGKeys fails t for every key of p that NG 1.1.2 would reject.
func checkNGKeys(t *testing.T, name string, p map[string]any, notification bool) {
	t.Helper()
	for k, v := range p {
		switch {
		case ngSharedKeys[k]:
		case ngNotifyOnlyKeys[k] && notification:
		default:
			t.Errorf("%s: key %q is not in NG's payload schema (notification=%v)", name, k, notification)
		}
		switch k {
		case "scroll":
			if m, ok := v.(map[string]any); ok {
				for sk := range m {
					if !ngScrollKeys[sk] {
						t.Errorf("%s: scroll.%s is not an NG scroll field", name, sk)
					}
				}
			}
		case "overlay":
			if s, _ := v.(string); !ngOverlays[s] {
				t.Errorf("%s: overlay %q is not an NG overlay name", name, v)
			}
		case "textCase":
			if s, _ := v.(string); !ngTextCases[s] {
				t.Errorf("%s: textCase %q is not an NG textCase value", name, v)
			}
		}
	}
}

// TestPayloadBuildersEmitOnlyNGKeys guards against a 422: every payload
// builder, in every variant, emits only keys NG 1.1.2 accepts for its route.
func TestPayloadBuildersEmitOnlyNGKeys(t *testing.T) {
	if n := len(ngSharedKeys) + len(ngNotifyOnlyKeys); n != 42 {
		t.Fatalf("key table has %d keys, NG documents 42", n)
	}
	hourly := []float64{3, 4, 5, 6, 7, 8}
	waiting := waitingSnap()
	running := Snapshot{Now: time.Now(), Sessions: []Session{{Source: "mbp", Tool: "claude",
		Session: "s1", State: "running", Activity: "Bash: go test", UpdatedAt: time.Now()}}}
	apps := map[string]map[string]any{
		"weather":         WeatherPayload(WeatherRain, "12°", hourly, 600),
		"weather-moon":    WeatherPayloadMoon("9°", hourly, MoonView{Illum: 0.5, Waxing: true}, 600),
		"weather-native":  WeatherPayloadNative("2422", "12°", hourly, 600),
		"weather-overlay": WithOverlay(WeatherPayload(WeatherStorm, "12°", hourly, 600), OverlayThunder),
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
	}
	for name, p := range popups {
		checkNGKeys(t, name, p, true)
	}
}
