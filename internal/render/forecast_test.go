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

func TestForecastBarsHeightAndColour(t *testing.T) {
	var f Frame
	hourly := []float64{0, 10, 20} // increasing → taller bars rightward
	drawForecastBars(&f, hourly, 0, 31)
	height := func(x int) int {
		h := 0
		for y := 0; y < 8; y++ {
			if f.Dirty[y][x] {
				h++
			}
		}
		return h
	}
	h0, h1, h2 := height(0), height(1), height(2)
	if !(h0 < h1 && h1 < h2) {
		t.Errorf("bar heights not increasing: %d,%d,%d", h0, h1, h2)
	}
	if h0 != 1 {
		t.Errorf("coldest bar height = %d, want 1", h0)
	}
	if h2 != 8 {
		t.Errorf("hottest bar height = %d, want 8", h2)
	}
	if !f.Dirty[7][0] {
		t.Error("bars must be anchored to the bottom row")
	}
	if f.Pixels[7][2] != TempColor(20) {
		t.Errorf("hottest bar colour = %v, want %v", f.Pixels[7][2], TempColor(20))
	}
}

func TestForecastBarsFlatWindowMidHeight(t *testing.T) {
	var f Frame
	drawForecastBars(&f, []float64{15, 15, 15}, 0, 31)
	h := 0
	for y := 0; y < 8; y++ {
		if f.Dirty[y][0] {
			h++
		}
	}
	if h != 4 {
		t.Errorf("flat-window bar height = %d, want 4 (mid)", h)
	}
}

func TestForecastPayloadFullWidthBars(t *testing.T) {
	p := ForecastPayload([]float64{10, 12, 14, 16, 18, 20}, 600)
	if p["lifetime"] != 600 {
		t.Fatalf("lifetime = %v, want 600", p["lifetime"])
	}
	if _, has := p["icon"]; has {
		t.Error("forecast tile must not carry a native icon (bars own the matrix)")
	}
	pixels := p["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// Bars stretch across the whole matrix: bottom row lit at both edges.
	if pixels[7*32+0] == 0 {
		t.Error("bars must start at col 0 (no icon/temp on the forecast tile)")
	}
	if pixels[7*32+31] == 0 {
		t.Error("bars must reach col 31 (stretched to full width)")
	}
	// No white temp digits anywhere (the temp lives on the conditions tile).
	white := (0xff << 16) | (0xff << 8) | 0xff
	for i, v := range pixels {
		if v == white {
			t.Fatalf("white temp pixel at %d — forecast tile must be bars only", i)
		}
	}
}

func TestForecastBarsScaledDistributesWidth(t *testing.T) {
	var f Frame
	// 4 bars over 32 cols → each bar 8 cols wide; warmest bar tallest.
	drawForecastBarsScaled(&f, []float64{0, 10, 20, 30}, 0, 31)
	for x := 0; x < 32; x++ {
		if !f.Dirty[7][x] {
			t.Fatalf("bottom row col %d not lit — bars must fill the width", x)
		}
	}
	// First bar (coldest) colour at col 0, last bar (warmest) at col 31.
	if f.Pixels[7][0] != TempColor(0) || f.Pixels[7][31] != TempColor(30) {
		t.Errorf("edge bar colours = %v/%v, want %v/%v",
			f.Pixels[7][0], f.Pixels[7][31], TempColor(0), TempColor(30))
	}
	// Warmest bar reaches the top row; coldest does not.
	if !f.Dirty[0][31] {
		t.Error("warmest bar should reach the top row")
	}
	if f.Dirty[0][0] {
		t.Error("coldest bar should not reach the top row")
	}
}

func TestWeatherPayloadIncludesTwoRowStrip(t *testing.T) {
	hourly := []float64{-5, 25}
	p := WeatherPayload(WeatherClear, "21°", hourly, 600)
	pixels := p["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	// The strip is 2px tall (rows 6-7), cols 9+.
	for _, row := range []int{6, 7} {
		if pixels[row*32+9] == 0 || pixels[row*32+10] == 0 {
			t.Errorf("weather tile strip row %d not drawn", row)
		}
	}
}

func TestWeatherTileCentersTemp(t *testing.T) {
	// "21°" = 3 glyphs = 11px wide; centred in cols 9-31 → starts at col 15.
	f := WeatherTileFrame(WeatherClear, "21°", nil, nil)
	for y := 0; y < 5; y++ {
		for x := 9; x < 15; x++ {
			if f.Dirty[y][x] {
				t.Fatalf("digit pixel at (%d,%d) — temp must be centred, not left-aligned", x, y)
			}
		}
	}
	lit := false
	for y := 0; y < 5; y++ {
		for x := 15; x <= 25; x++ {
			if f.Dirty[y][x] {
				lit = true
			}
		}
	}
	if !lit {
		t.Error("centred temp digits not drawn")
	}
}
