package render

// Hourly temperature forecast rendering: a cold→warm colour gradient, a compact
// one-pixel-per-hour strip drawn on the weather tile, and a standalone forecast
// tile of vertical bars (height + colour = temperature). Inspired by the
// blueforcer AWTRIX weather flow, but key-less: temps come from the existing
// providers and the gradient/heights are computed here.

// tempStop is one anchor of the temperature→colour gradient.
type tempStop struct {
	t float64
	c RGB
}

// tempGradient maps °C to colour, cold (blue) → warm (red). Values between
// stops are linearly interpolated per channel; below/above the ends clamp.
var tempGradient = []tempStop{
	{-10, RGB{0x3B, 0x4C, 0xFF}}, // deep blue
	{0, RGB{0x4F, 0xA9, 0xFF}},   // light blue
	{10, RGB{0x3D, 0xD6, 0x8C}},  // green
	{20, RGB{0xF2, 0xC4, 0x4D}},  // amber
	{30, RGB{0xFF, 0x7A, 0x33}},  // orange
	{38, RGB{0xE0, 0x33, 0x33}},  // red
}

// TempColor returns the gradient colour for a temperature in °C. Exported so the
// caller can keep the strip and tile consistent with any future text colouring.
func TempColor(c float64) RGB {
	last := len(tempGradient) - 1
	if c <= tempGradient[0].t {
		return tempGradient[0].c
	}
	if c >= tempGradient[last].t {
		return tempGradient[last].c
	}
	for i := 0; i < last; i++ {
		a, b := tempGradient[i], tempGradient[i+1]
		if c >= a.t && c <= b.t {
			f := (c - a.t) / (b.t - a.t)
			return RGB{lerp(a.c.R, b.c.R, f), lerp(a.c.G, b.c.G, f), lerp(a.c.B, b.c.B, f)}
		}
	}
	return tempGradient[last].c
}

// lerp linearly interpolates between two channel values (rounded).
func lerp(a, b uint8, f float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*f + 0.5)
}

// hourSlot returns the columns hour i of an n-hour window owns on the
// bottom-bar grid (cols barX0..31). Every hour gets the same whole number of
// columns, barW/n, laid out left to right from barX0, so hour 0 is always at
// col 8 and hour i sits in the same columns on the weather strip, the air
// strip and the forecast tile. Windows that divide 24 (6, 8, 12, 24 h) fill
// the bar; any other window leaves its remainder as a dark tail at the right
// (22 h: cols 30-31) rather than doubling some hours and not others.
func hourSlot(i, n int) (x0, x1 int) {
	w := barW / n
	if w < 1 {
		w = 1
	}
	x0 = barX0 + i*w
	return x0, x0 + w - 1
}

// drawHourlyStrip paints an hourly strip on the bottom bar (row 7), hour i in
// its hourSlot coloured colour(i). n <= 0 is a no-op; hours past col 31 are
// cut.
func drawHourlyStrip(f *Frame, n int, colour func(i int) RGB) {
	for i := 0; i < n; i++ {
		x0, x1 := hourSlot(i, n)
		if x0 >= panelW {
			break
		}
		paintRow(f, x0, min(x1, panelW-1), barRow, colour(i))
	}
}

// drawForecastStrip paints the hourly temperatures as the bottom-bar strip.
func drawForecastStrip(f *Frame, hourly []float64) {
	drawHourlyStrip(f, len(hourly), func(i int) RGB { return TempColor(hourly[i]) })
}

// centredX is the start column that centres a 3×5 string in the content area
// (cols contentX..31), clamped to contentX when it overflows.
func centredX(text string) int {
	x := contentX + (contentW-(len([]rune(text))*4-1))/2
	if x < contentX {
		x = contentX
	}
	return x
}

// drawForecastBars paints one vertical bar per hour across cols x0..x1
// (inclusive), bottom-anchored, height normalised to the shown window's min/max
// (1..8 px) and coloured by TempColor. A flat window draws mid-height bars.
func drawForecastBars(f *Frame, hourly []float64, x0, x1 int) {
	if x0 > x1 || len(hourly) == 0 {
		return
	}
	n := len(hourly)
	if w := x1 - x0 + 1; n > w {
		n = w
	}
	min, max := hourly[0], hourly[0]
	for _, t := range hourly[:n] {
		if t < min {
			min = t
		}
		if t > max {
			max = t
		}
	}
	span := max - min
	for i := 0; i < n; i++ {
		t := hourly[i]
		h := 4 // flat window → mid-height
		if span >= 1e-9 {
			h = 1 + int((t-min)/span*7.0+0.5) // 1..8
		}
		col := TempColor(t)
		for y := 8 - h; y < 8; y++ {
			paintCell(f, x0+i, y, col)
		}
	}
}

// drawForecastBarsOnGrid paints one bottom-anchored bar per hour, hour i
// filling its hourSlot, so every bar is the same width and each hour lines up
// with its column in the weather strip (24 h: one 1-px bar per col, 8-31).
// Same height/colour rules as drawForecastBars. Used by the forecast tile.
func drawForecastBarsOnGrid(f *Frame, hourly []float64) {
	n := len(hourly)
	if n == 0 {
		return
	}
	if n > barW {
		n = barW // more hours than columns: the tail is cut
	}
	min, max := hourly[0], hourly[0]
	for _, t := range hourly[:n] {
		if t < min {
			min = t
		}
		if t > max {
			max = t
		}
	}
	span := max - min
	for i := 0; i < n; i++ {
		t := hourly[i]
		h := 4 // flat window → mid-height
		if span >= 1e-9 {
			h = 1 + int((t-min)/span*7.0+0.5) // 1..8
		}
		col := TempColor(t)
		xs, xe := hourSlot(i, n)
		for x := xs; x <= xe; x++ {
			for y := 8 - h; y < 8; y++ {
				paintCell(f, x, y, col)
			}
		}
	}
}

// ForecastTileFrame composes the drawn forecast-tile frame: the hourly
// temperature bars on the bottom-bar grid (cols 8-31, full height) — no icon,
// no temp digits (those live on the conditions tile), so the two tiles read
// differently at a glance. Shared by the device payload and
// /v1/weather/preview.
func ForecastTileFrame(hourly []float64) Frame {
	var f Frame
	drawForecastBarsOnGrid(&f, hourly)
	return f
}

// ForecastPayload renders the standalone forecast tile: hourly temperature
// bars (height + colour = temperature). lifetime seconds.
func ForecastPayload(hourly []float64, lifetime int) map[string]any {
	f := ForecastTileFrame(hourly)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}
