package render

import "testing"

func TestWeatherPayloadHasDrawAndTemp(t *testing.T) {
	p := WeatherPayload(WeatherRain, "21°", nil, 600)
	if p["lifetimeMs"] != 600_000 {
		t.Fatalf("lifetimeMs = %v, want 600000", p["lifetimeMs"])
	}
	if draw, ok := p["draw"].([]any); !ok || len(draw) != 1 {
		t.Fatalf("draw op missing: %v", p["draw"])
	}
	// The frame must light some pixels for both the icon (cols 0–7) and the
	// temperature digits (from col 9), proving both regions drew.
	pixels := bmpPixels(t, p)
	if len(pixels) != 256 {
		t.Fatalf("frame pixel count = %d, want 256", len(pixels))
	}
	iconLit, textLit := false, false
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			if pixels[y*32+x] == 0 {
				continue
			}
			if x < 8 {
				iconLit = true
			}
			if x >= 9 {
				textLit = true
			}
		}
	}
	if !iconLit {
		t.Error("no icon pixels lit in cols 0–7")
	}
	if !textLit {
		t.Error("no temperature pixels lit from col 9")
	}
}

func TestWeatherPopupDrawnVsNative(t *testing.T) {
	drawn := WeatherPopupPayload(WeatherStorm, "STORM 18°", "", 30)
	if _, has := drawn["draw"]; !has {
		t.Error("drawn popup must carry a draw op")
	}
	if drawn["textCenter"] != false || drawn["textOffsetX"] != 9 {
		t.Errorf("drawn popup must set textCenter:false textOffsetX:9, got center=%v offset=%v", drawn["textCenter"], drawn["textOffsetX"])
	}
	if _, has := drawn["icon"]; has {
		t.Error("drawn popup must not set a native icon")
	}

	native := WeatherPopupPayload(WeatherClear, "CLEAR 25°", "2422", 30)
	if native["icon"] != "2422" {
		t.Errorf("native popup icon = %v, want 2422", native["icon"])
	}
	if _, has := native["draw"]; has {
		t.Error("native popup must not draw an icon (firmware reserves the slot)")
	}
	if _, has := native["textOffsetX"]; has {
		t.Error("native popup must not set textOffsetX (double-shift clip)")
	}
}

func TestWeatherPopupNeverCarriesSound(t *testing.T) {
	// The chime is played separately (firmware drops a notification's sound under
	// an icon), so the popup payload must never carry a sound field.
	p := WeatherPopupPayload(WeatherStorm, "STORM", "", 30)
	if _, has := p["sound"]; has {
		t.Error("weather popup must not carry a sound field")
	}
}

// TestHexOf pins the one colour format Ember emits. NG also accepts bare
// "RRGGBB", but a single canonical spelling keeps every payload comparable and
// matches what the device echoes back on reads.
func TestHexOf(t *testing.T) {
	if got := hexOf(RGB{0x4f, 0xa9, 0xff}); got != "#4FA9FF" {
		t.Errorf("hexOf = %q, want #4FA9FF", got)
	}
	if got := hexOf(RGB{0, 0, 0}); got != "#000000" {
		t.Errorf("hexOf black = %q, want #000000", got)
	}
}

// TestEveryColorFieldIsCanonicalHex sweeps the colour-bearing builders and
// insists each colour field is exactly "#RRGGBB" — the mix of "RRGGBB" and
// "#RRGGBB" that AWTRIX3 tolerated is gone.
func TestEveryColorFieldIsCanonicalHex(t *testing.T) {
	colorFields := []string{"textColor", "progressColor", "progressTrackColor",
		"backgroundColor", "chartColor"}
	payloads := map[string]map[string]any{
		"WeatherPopupPayload":    WeatherPopupPayload(WeatherStorm, "STORM", "", 30),
		"AirPopupPayload":        AirPopupPayload(85, 30),
		"MeetingPayload":         MeetingPayload("STANDUP", 5, 600),
		"MeetingPopupPayload":    MeetingPopupPayload("STANDUP", 5, 30),
		"ReminderPopupPayload":   ReminderPopupPayload("STAND UP", "", 10, true),
		"SunPopupPayload":        SunPopupPayload(false, "SUNSET 21:04", 30),
		"LimitResetPopupPayload": LimitResetPopupPayload("codex", 10),
		"PomodoroPayload":        PomodoroPayload(PomodoroView{Phase: pomoFocus, RemainingSec: 90, PlannedSec: 1500}, 30),
		"detailPayload":          detailPayload(Session{Tool: "claude", State: "running"}, "X", stateHex("running"), false, 30, false),
	}
	for name, p := range payloads {
		for _, k := range colorFields {
			v, has := p[k]
			if !has {
				continue
			}
			s, ok := v.(string)
			if !ok || !isHexColor(s) {
				t.Errorf("%s[%s] = %v, want a canonical \"#RRGGBB\" string", name, k, v)
			}
		}
	}
}

func TestDegreeGlyphRenders(t *testing.T) {
	var f Frame
	drawDigits(&f, "°", 0, 0, colorWhite)
	lit := false
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			if f.Dirty[y][x] {
				lit = true
			}
		}
	}
	if !lit {
		t.Error("degree glyph drew nothing — missing from font3x5")
	}
}

func TestWeatherColorDistinct(t *testing.T) {
	// Each bucket should have a recognisably different colour from clouds.
	base := WeatherColor(WeatherClouds)
	for _, cond := range []string{WeatherClear, WeatherRain, WeatherSnow, WeatherStorm} {
		if WeatherColor(cond) == base {
			t.Errorf("%s shares the clouds colour", cond)
		}
	}
}

func TestWeatherPayloadNative(t *testing.T) {
	p := WeatherPayloadNative("2286", "21°", []float64{18, 19, 20}, 90)
	if p["icon"] != "2286" {
		t.Errorf("icon = %v, want 2286", p["icon"])
	}
	if _, has := p["text"]; has {
		t.Errorf("text must be absent (digits are drawn, not native text)")
	}
	if p["lifetimeMs"] != 90_000 || p["durationMs"] != msOf(rotateDwellSeconds) {
		t.Errorf("lifetimeMs/durationMs = %v/%v, want 90000/%d", p["lifetimeMs"], p["durationMs"], msOf(rotateDwellSeconds))
	}
	op := bmp(t, p, 0)
	if op[1] != 8 || op[2] != 0 || op[3] != 24 || op[4] != 8 {
		t.Fatalf("bitmap rect = %v %v %v %v, want 8 0 24 8 (icon cols left alone)", op[1], op[2], op[3], op[4])
	}
	px := bmpPixels(t, p)
	if len(px) != 192 {
		t.Fatalf("partial bitmap len = %d, want 192", len(px))
	}
	sum := 0
	for _, v := range px {
		sum += v
	}
	if sum == 0 {
		t.Errorf("partial bitmap is empty — digits/strip not drawn")
	}
}

func TestWeatherTileFrame_MatchesPayloadPixels(t *testing.T) {
	hourly := []float64{18, 19, 20}
	f := WeatherTileFrame(WeatherRain, "21°", hourly, nil)
	p := WeatherPayload(WeatherRain, "21°", hourly, 90)
	want := bmpPixels(t, p)
	if got := framePixels(&f); !slicesEqualInt(got, want) {
		t.Errorf("exported weather frame diverges from the payload bitmap")
	}
	ff := ForecastTileFrame(hourly)
	pf := ForecastPayload(hourly, 90)
	wantF := bmpPixels(t, pf)
	if got := framePixels(&ff); !slicesEqualInt(got, wantF) {
		t.Errorf("exported forecast frame diverges from the payload bitmap")
	}
	if hp := HexPixels(&f); len(hp) != 256 || hp[0][0] != '#' {
		t.Errorf("HexPixels: len %d / first %q, want 256 / #-prefixed", len(hp), hp[0])
	}
}

func slicesEqualInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
