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

func TestForecastStripLightsOneColumnPerHourFor24h(t *testing.T) {
	var f Frame
	hourly := make([]float64, barW)
	for i := range hourly {
		hourly[i] = float64(i*2 - 10)
	}
	drawForecastStrip(&f, hourly)
	for i := range hourly {
		x := barX0 + i
		if f.Pixels[barRow][x] != TempColor(hourly[i]) {
			t.Errorf("strip col %d colour = %v, want hour %d %v", x, f.Pixels[barRow][x], i, TempColor(hourly[i]))
		}
	}
	// The icon cols and the rows above the bar are untouched by the strip.
	for y := 0; y < barRow; y++ {
		for x := 0; x < panelW; x++ {
			if f.Dirty[y][x] {
				t.Errorf("strip painted above the bottom row at (%d,%d)", x, y)
			}
		}
	}
	for x := 0; x < barX0; x++ {
		if f.Dirty[barRow][x] {
			t.Errorf("strip painted col %d, left of the bar", x)
		}
	}
}

func TestForecastStripStretchesShortWindows(t *testing.T) {
	var f Frame
	hourly := []float64{-5, 0, 10, 20, 30, 35} // 6 h → 4 columns each
	drawForecastStrip(&f, hourly)
	for x := barX0; x < panelW; x++ {
		i := (x - barX0) / 4
		if f.Pixels[barRow][x] != TempColor(hourly[i]) {
			t.Errorf("strip col %d colour = %v, want hour %d %v", x, f.Pixels[barRow][x], i, TempColor(hourly[i]))
		}
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
	if p["lifetimeMs"] != 600_000 {
		t.Fatalf("lifetimeMs = %v, want 600000", p["lifetimeMs"])
	}
	if _, has := p["icon"]; has {
		t.Error("forecast tile must not carry a native icon (bars own the matrix)")
	}
	pixels := bmpPixels(t, p)
	// Six 5-px bars span 30 cols, centred: cols 1-30.
	if pixels[7*32+1] == 0 || pixels[7*32+30] == 0 {
		t.Error("bars must span cols 1-30 (six 5-px bars, centred)")
	}
	if pixels[7*32+0] != 0 || pixels[7*32+31] != 0 {
		t.Error("the 2 spare cols must be split as margins")
	}
	// No white temp digits anywhere (the temp lives on the conditions tile).
	white := (0xff << 16) | (0xff << 8) | 0xff
	for i, v := range pixels {
		if v == white {
			t.Fatalf("white temp pixel at %d — forecast tile must be bars only", i)
		}
	}
}

// TestForecastBarsHaveEvenWidths pins every bar of a window to the same
// width. Spreading 24 hours over 32 columns made every third bar 2 px and the
// rest 1 px, so the chart looked jagged; the remainder now goes to margins.
func TestForecastBarsHaveEvenWidths(t *testing.T) {
	for _, n := range []int{6, 12, 16, 20, 22, 24} {
		temps := make([]float64, n)
		for i := range temps {
			temps[i] = float64(i * 30 / n)
		}
		f := ForecastTileFrame(temps)
		w := panelW / n
		x0 := (panelW - w*n) / 2
		for x := 0; x < panelW; x++ {
			inside := x >= x0 && x < x0+w*n
			if f.Dirty[barRow][x] != inside {
				t.Errorf("%dh: col %d lit=%v, want %v (bars cols %d-%d)", n, x, f.Dirty[barRow][x], inside, x0, x0+w*n-1)
			}
			if !inside {
				continue
			}
			i := (x - x0) / w
			if got := f.Pixels[barRow][x]; got != TempColor(temps[i]) {
				t.Errorf("%dh: col %d = %v, want bar %d %v", n, x, got, i, TempColor(temps[i]))
			}
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

func TestWeatherPayloadIncludesBottomBarStrip(t *testing.T) {
	hourly := []float64{-5, 25}
	p := WeatherPayload(WeatherClear, "21°", hourly, 600)
	pixels := bmpPixels(t, p)
	// The strip is the 1px bottom bar (row 7, cols 8-31); row 6 is a spacer.
	if pixels[barRow*32+barX0] == 0 || pixels[barRow*32+31] == 0 {
		t.Error("weather tile strip not drawn across the bottom bar")
	}
	for x := contentX; x < panelW; x++ {
		if pixels[6*32+x] != 0 {
			t.Errorf("row 6 col %d lit, want a spacer above the strip", x)
		}
	}
}

func TestWeatherTileCentersTemp(t *testing.T) {
	// "21°" = 3 glyphs = 11px wide; centred in cols 9-31 → starts at col 15.
	f := WeatherTileFrame(WeatherClear, "21°", nil, nil)
	for y := 0; y < 6; y++ {
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
