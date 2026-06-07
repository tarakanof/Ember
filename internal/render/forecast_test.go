package render

import "testing"

func TestTempColorClampsAndInterpolates(t *testing.T) {
	// Below the coldest stop clamps to it; above the warmest clamps to it.
	if got := TempColor(-50); got != tempGradient[0].c {
		t.Errorf("cold clamp = %v, want %v", got, tempGradient[0].c)
	}
	if got := TempColor(100); got != tempGradient[len(tempGradient)-1].c {
		t.Errorf("warm clamp = %v, want %v", got, tempGradient[len(tempGradient)-1].c)
	}
	// An exact stop returns that stop's colour.
	if got := TempColor(20); got != (RGB{0xF2, 0xC4, 0x4D}) {
		t.Errorf("TempColor(20) = %v, want amber stop", got)
	}
	// The midpoint between 0°C (#4FA9FF) and 10°C (#3DD68C) interpolates each
	// channel halfway.
	mid := TempColor(5)
	wantR := uint8((0x4F + 0x3D + 1) / 2) // rounded
	if mid.R != wantR {
		t.Errorf("TempColor(5).R = %d, want ~%d (halfway 4F→3D)", mid.R, wantR)
	}
	// Warmer should be redder than colder across the range.
	if TempColor(35).R <= TempColor(-5).R {
		t.Errorf("warm temp should have a higher red channel than cold")
	}
}

func TestForecastStripLightsOnePixelPerHour(t *testing.T) {
	var f Frame
	hourly := []float64{-5, 0, 10, 20, 30}
	drawForecastStrip(&f, hourly, 9, 31, 7)
	for i := range hourly {
		x := 9 + i
		if !f.Dirty[7][x] {
			t.Errorf("strip pixel at col %d not lit", x)
		}
		if f.Pixels[7][x] != TempColor(hourly[i]) {
			t.Errorf("strip col %d colour = %v, want %v", x, f.Pixels[7][x], TempColor(hourly[i]))
		}
	}
	// Nothing past the data, and the icon/temp rows are untouched by the strip.
	if f.Dirty[7][9+len(hourly)] {
		t.Error("strip lit a pixel past the data")
	}
	for y := 0; y < 7; y++ {
		if f.Dirty[y][9] {
			t.Errorf("strip painted above the bottom row at y=%d", y)
		}
	}
}

func TestForecastStripCapsAtX1(t *testing.T) {
	var f Frame
	// 30 hours but only cols 9..31 (23) available — must stop at x1.
	hourly := make([]float64, 30)
	drawForecastStrip(&f, hourly, 9, 31, 7)
	lit := 0
	for x := 0; x < 32; x++ {
		if f.Dirty[7][x] {
			lit++
		}
	}
	if lit != 23 {
		t.Errorf("strip lit %d cols, want 23 (capped at x1)", lit)
	}
}

func TestForecastPayloadBarsHeightAndColour(t *testing.T) {
	hourly := []float64{0, 10, 20} // increasing → taller bars rightward
	p := ForecastPayload(hourly, 600)
	if p["lifetime"] != 600 {
		t.Fatalf("lifetime = %v, want 600", p["lifetime"])
	}
	draw := p["draw"].([]any)
	op := draw[0].(map[string]any)["db"].([]any)
	pixels := op[4].([]int)
	if len(pixels) != 256 {
		t.Fatalf("frame pixel count = %d, want 256", len(pixels))
	}
	// Height per column: count lit pixels in each column (bottom-anchored).
	height := func(x int) int {
		h := 0
		for y := 0; y < 8; y++ {
			if pixels[y*32+x] != 0 {
				h++
			}
		}
		return h
	}
	h0, h1, h2 := height(0), height(1), height(2)
	if !(h0 < h1 && h1 < h2) {
		t.Errorf("bar heights not increasing: %d,%d,%d", h0, h1, h2)
	}
	// Coldest bar = min height 1, hottest = full 8.
	if h0 != 1 {
		t.Errorf("coldest bar height = %d, want 1", h0)
	}
	if h2 != 8 {
		t.Errorf("hottest bar height = %d, want 8", h2)
	}
	// Bars are bottom-anchored: the bottom pixel of col 0 is lit.
	if pixels[7*32+0] == 0 {
		t.Error("bars must be anchored to the bottom row")
	}
	// No data past the last hour.
	if height(3) != 0 {
		t.Error("forecast tile lit a column past the data")
	}
}

func TestForecastPayloadFlatWindowMidHeight(t *testing.T) {
	p := ForecastPayload([]float64{15, 15, 15}, 600)
	pixels := p["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	h := 0
	for y := 0; y < 8; y++ {
		if pixels[y*32+0] != 0 {
			h++
		}
	}
	if h != 4 {
		t.Errorf("flat-window bar height = %d, want 4 (mid)", h)
	}
}

func TestWeatherPayloadIncludesStrip(t *testing.T) {
	hourly := []float64{-5, 25}
	p := WeatherPayload(WeatherClear, "21°", hourly, 600)
	pixels := p["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// Bottom row, col 9 and 10 should carry the strip colours.
	if pixels[7*32+9] == 0 || pixels[7*32+10] == 0 {
		t.Error("weather tile bottom-row strip not drawn")
	}
}
