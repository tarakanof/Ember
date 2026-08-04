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

// drawForecastStrip paints one pixel per hourly temperature across row y from
// x0..x1 (inclusive), each coloured by TempColor. Draws min(len(hourly),
// x1-x0+1) columns; a nil/empty slice is a no-op.
func drawForecastStrip(f *Frame, hourly []float64, x0, x1, y int) {
	x := x0
	for _, t := range hourly {
		if x > x1 {
			break
		}
		paintCell(f, x, y, TempColor(t))
		x++
	}
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

// drawForecastBarsScaled paints the hourly bars stretched across cols x0..x1:
// each hour gets an even share of the width (some bars 1px wider than others
// when it doesn't divide evenly), bottom-anchored, same height/colour rules as
// drawForecastBars. Used by the full-matrix forecast tile.
func drawForecastBarsScaled(f *Frame, hourly []float64, x0, x1 int) {
	if x0 > x1 || len(hourly) == 0 {
		return
	}
	n := len(hourly)
	w := x1 - x0 + 1
	if n > w {
		n = w // more hours than columns: 1px each, the tail is cut
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
		xs, xe := x0+i*w/n, x0+(i+1)*w/n-1
		for x := xs; x <= xe; x++ {
			for y := 8 - h; y < 8; y++ {
				paintCell(f, x, y, col)
			}
		}
	}
}

// ForecastTileFrame composes the drawn forecast-tile frame: the hourly
// temperature bars stretched across the full 32-col matrix — no icon, no temp
// digits (those live on the conditions tile), so the two tiles read
// differently at a glance. Shared by the device payload and
// /v1/weather/preview.
func ForecastTileFrame(hourly []float64) Frame {
	var f Frame
	drawForecastBarsScaled(&f, hourly, 0, 31)
	return f
}

// ForecastPayload renders the standalone forecast tile: full-width hourly
// temperature bars (height + colour = temperature). lifetime seconds.
func ForecastPayload(hourly []float64, lifetime int) map[string]any {
	f := ForecastTileFrame(hourly)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}
