package render

import "testing"

func TestWeatherPayloadHasDrawAndTemp(t *testing.T) {
	p := WeatherPayload(WeatherRain, "21°", nil, 600)
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

func TestWeatherPayloadNative(t *testing.T) {
	p := WeatherPayloadNative("2286", "21°", []float64{18, 19, 20}, 90)
	if p["icon"] != "2286" {
		t.Errorf("icon = %v, want 2286", p["icon"])
	}
	if _, has := p["text"]; has {
		t.Errorf("text must be absent (digits are drawn, not native text)")
	}
	if p["lifetime"] != 90 || p["duration"] != rotateDwellSeconds {
		t.Errorf("lifetime/duration = %v/%v, want 90/%d", p["lifetime"], p["duration"], rotateDwellSeconds)
	}
	db := p["draw"].([]any)[0].(map[string]any)["db"].([]any)
	if db[0] != 8 || db[1] != 0 || db[2] != 24 || db[3] != 8 {
		t.Fatalf("db rect = %v %v %v %v, want 8 0 24 8 (icon cols left alone)", db[0], db[1], db[2], db[3])
	}
	px := db[4].([]int)
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
	want := p["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	if got := framePixels(&f); !slicesEqualInt(got, want) {
		t.Errorf("exported weather frame diverges from the payload bitmap")
	}
	ff := ForecastTileFrame(WeatherRain, "21°", hourly)
	pf := ForecastPayload(WeatherRain, "21°", hourly, 90)
	wantF := pf["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
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
