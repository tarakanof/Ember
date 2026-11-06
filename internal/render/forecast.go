package render

type tempStop struct {
	t float64
	c RGB
}

var tempGradient = []tempStop{
	{-10, RGB{0x3B, 0x4C, 0xFF}},
	{0, RGB{0x4F, 0xA9, 0xFF}},
	{10, RGB{0x3D, 0xD6, 0x8C}},
	{20, RGB{0xF2, 0xC4, 0x4D}},
	{30, RGB{0xFF, 0x7A, 0x33}},
	{38, RGB{0xE0, 0x33, 0x33}},
}

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

func lerp(a, b uint8, f float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*f + 0.5)
}

func hourSlot(i, n int) (x0, x1 int) {
	w := barW / n
	if w < 1 {
		w = 1
	}
	x0 = barX0 + i*w
	return x0, x0 + w - 1
}

func drawHourlyStrip(f *Frame, n int, colour func(i int) RGB) {
	for i := 0; i < n; i++ {
		x0, x1 := hourSlot(i, n)
		if x0 >= panelW {
			break
		}
		paintRow(f, x0, min(x1, panelW-1), barRow, colour(i))
	}
}

func drawForecastStrip(f *Frame, hourly []float64) {
	drawHourlyStrip(f, len(hourly), func(i int) RGB { return TempColor(hourly[i]) })
}

func centredX(text string) int {
	x := contentX + (contentW-(len([]rune(text))*4-1))/2
	if x < contentX {
		x = contentX
	}
	return x
}

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
		h := 4
		if span >= 1e-9 {
			h = 1 + int((t-min)/span*7.0+0.5)
		}
		col := TempColor(t)
		for y := 8 - h; y < 8; y++ {
			paintCell(f, x0+i, y, col)
		}
	}
}

func drawForecastBarsOnGrid(f *Frame, hourly []float64) {
	n := len(hourly)
	if n == 0 {
		return
	}
	if n > barW {
		n = barW
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
		h := 4
		if span >= 1e-9 {
			h = 1 + int((t-min)/span*7.0+0.5)
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

func ForecastTileFrame(hourly []float64) Frame {
	var f Frame
	drawForecastBarsOnGrid(&f, hourly)
	return f
}

func ForecastPayload(hourly []float64, lifetime int) map[string]any {
	f := ForecastTileFrame(hourly)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}
