package render

import "testing"

func TestWeatherPayloadHasDrawAndTemp(t *testing.T) {
	p := WeatherPayload(WeatherRain, "21°", 600)
	if p["lifetime"] != 600 {
		t.Fatalf("lifetime = %v, want 600", p["lifetime"])
	}
	draw, ok := p["draw"].([]any)
	if !ok || len(draw) != 1 {
		t.Fatalf("draw op missing: %v", p["draw"])
	}
	// The frame must light some pixels for both the icon (cols 0–7) and the
	// temperature digits (from col 9), proving both regions drew.
	op := draw[0].(map[string]any)["db"].([]any)
	pixels := op[4].([]int)
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
	if drawn["center"] != false || drawn["textOffset"] != 9 {
		t.Errorf("drawn popup must set center:false textOffset:9, got center=%v offset=%v", drawn["center"], drawn["textOffset"])
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
	if _, has := native["textOffset"]; has {
		t.Error("native popup must not set textOffset (double-shift clip)")
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

func TestHexOf(t *testing.T) {
	if got := hexOf(RGB{0x4f, 0xa9, 0xff}); got != "4FA9FF" {
		t.Errorf("hexOf = %q, want 4FA9FF", got)
	}
	if got := hexOf(RGB{0, 0, 0}); got != "000000" {
		t.Errorf("hexOf black = %q, want 000000", got)
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
