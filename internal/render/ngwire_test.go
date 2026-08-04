package render

import (
	"encoding/json"
	"testing"
	"time"
)

// bmp returns the i-th draw command of p, asserting it is NG's array-form
// ["bitmap", x, y, w, h, data] (AWTRIX3's {"db":[…]} object form is a 422 on NG).
func bmp(t *testing.T, p map[string]any, i int) []any {
	t.Helper()
	draw, ok := p["draw"].([]any)
	if !ok || i >= len(draw) {
		t.Fatalf("draw[%d] missing: %v", i, p["draw"])
	}
	op, ok := draw[i].([]any)
	if !ok || len(op) != 6 || op[0] != "bitmap" {
		t.Fatalf(`draw[%d] = %v, want ["bitmap", x, y, w, h, data]`, i, draw[i])
	}
	return op
}

// bmpPixels returns the packed-int pixel data of p's first bitmap draw command.
func bmpPixels(t *testing.T, p map[string]any) []int {
	t.Helper()
	px, ok := bmp(t, p, 0)[5].([]int)
	if !ok {
		t.Fatalf("bitmap data is not []int: %v", bmp(t, p, 0)[5])
	}
	return px
}

// legacyKeys are the AWTRIX3 spellings awtrix-ng rejects with 422
// (validationFailed, field = the key). No builder may emit any of them.
var legacyKeys = []string{
	"duration", "lifetime", "lifetimeMode", "color", "center", "textOffset",
	"noScroll", "blinkText", "progressC", "progressBC", "pushIcon", "rtttl",
	"loopSound", "prio", "force", "save", "pos", "topText", "bar", "line",
	"autoscale", "background", "fadeText", "rainbow", "gradient", "scrollSpeed",
	"clients", "barBC", "effectSettings",
}

// assertNGPayload fails when a builder's payload carries an AWTRIX3 key or an
// object-form draw command, and enforces NG's 8192-byte body limit.
func assertNGPayload(t *testing.T, name string, p map[string]any) {
	t.Helper()
	for _, k := range legacyKeys {
		if _, has := p[k]; has {
			t.Errorf("%s: legacy AWTRIX3 key %q present — awtrix-ng 422s the whole payload", name, k)
		}
	}
	if draw, ok := p["draw"].([]any); ok {
		for i, op := range draw {
			if _, isObj := op.(map[string]any); isObj {
				t.Errorf("%s: draw[%d] is an object — NG draw commands are arrays", name, i)
			}
		}
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("%s: payload is not JSON-encodable: %v", name, err)
	}
	if len(body) > ngMaxPayloadBytes {
		t.Errorf("%s: payload is %d bytes, over NG's %d-byte body limit", name, len(body), ngMaxPayloadBytes)
	}
}

// TestAllBuildersEmitNGSchema is the cross-builder guard: every payload builder
// in this package must survive assertNGPayload. A new builder that reaches for
// an AWTRIX3 key fails here even if its own test forgets to check.
func TestAllBuildersEmitNGSchema(t *testing.T) {
	var f Frame
	paintCell(&f, 0, 0, RGB{0xff, 0x00, 0x00})
	sess := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "waiting",
		Activity: "Bash: npm test", UpdatedAt: time.Now()}
	snap := Snapshot{Now: time.Now(), Sessions: []Session{sess}}
	hourly := []float64{1, 2, 3, 4, 5}
	pct := 40
	view := &UsageView{FiveHourPct: 80, ResetLabel: "14:30", SevenDayPct: &pct,
		Models: []ModelUsage{{"OP", 50}, {"SO", 20}}}

	for _, tc := range []struct {
		name string
		p    map[string]any
	}{
		{"frameToCustomApp/hold", frameToCustomApp(&f, 30, true)},
		{"frameToCustomApp/rotate", frameToCustomApp(&f, 30, false)},
		{"detailPayload/blink", detailPayload(sess, "WAIT MBP", "#FFC14D", true, 30, true)},
		{"detailPayload/detail", detailPayload(sess, "Bash: npm test", "#2EE85E", false, 30, false)},
		{"RenderForCoord", RenderForCoord(snap, sess.Key(), cardSource, false, 30, nil)},
		{"RenderForCoord/locked", RenderForCoord(snap, sess.Key(), cardSource, true, 30, nil)},
		{"RenderIdleFrame", RenderIdleFrame(30)},
		{"RenderIdleUsagePayload", RenderIdleUsagePayload(map[string]*UsageView{"claude": view}, 0, time.Now(), 30)},
		{"PomodoroPayload", PomodoroPayload(PomodoroView{Phase: pomoFocus, RemainingSec: 90, PlannedSec: 1500}, 30)},
		{"WeatherPayload", WeatherPayload(WeatherClear, "21°", hourly, 600)},
		{"WeatherPayloadMoon", WeatherPayloadMoon("21°", hourly, MoonView{Illum: 0.5, Waxing: true}, 600)},
		{"WeatherPayloadNative", WeatherPayloadNative("1234", "21°", hourly, 600)},
		{"WeatherPopupPayload/drawn", WeatherPopupPayload(WeatherStorm, "STORM", "", 30)},
		{"WeatherPopupPayload/native", WeatherPopupPayload(WeatherStorm, "STORM", "1234", 30)},
		{"ForecastPayload", ForecastPayload(hourly, 600)},
		{"AirPayload", AirPayload(85, hourly, 600)},
		{"AirPopupPayload", AirPopupPayload(85, 30)},
		{"MeetingPayload", MeetingPayload("STANDUP", 5, 600)},
		{"MeetingPopupPayload", MeetingPopupPayload("STANDUP", 5, 30)},
		{"ReminderPopupPayload/drawn", ReminderPopupPayload("STAND UP", "", 10, true)},
		{"ReminderPopupPayload/native", ReminderPopupPayload("STAND UP", "1234", 10, false)},
		{"SunPopupPayload", SunPopupPayload(true, "SUNRISE 5:21", 30)},
		{"LimitResetPopupPayload", LimitResetPopupPayload("claude", 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.p == nil {
				t.Fatal("builder returned nil")
			}
			assertNGPayload(t, tc.name, tc.p)
		})
	}
}

// TestFullFrameBitmapFitsPayloadBudget pins the largest builder output — a 32×8
// full-frame bitmap where every pixel is a distinct 6-digit colour, the
// worst-case serialisation — under NG's 8192-byte body limit.
func TestFullFrameBitmapFitsPayloadBudget(t *testing.T) {
	var f Frame
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			// 0xFFxxxx keeps every packed int at its 8-decimal-digit maximum.
			paintCell(&f, x, y, RGB{0xff, uint8(y*32 + x), uint8(255 - x)})
		}
	}
	body, err := json.Marshal(frameToCustomApp(&f, 30, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(body) > ngMaxPayloadBytes {
		t.Fatalf("worst-case full-frame payload is %d bytes, over the %d-byte limit",
			len(body), ngMaxPayloadBytes)
	}
	t.Logf("worst-case full-frame payload: %d bytes (limit %d)", len(body), ngMaxPayloadBytes)
}
