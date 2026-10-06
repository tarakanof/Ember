package render

type MoonView struct {
	Illum  float64
	Waxing bool
}

var moonColor = RGB{0xE8, 0xE6, 0xC0}

var moonDisc = [8][2]int{{2, 5}, {1, 6}, {0, 7}, {0, 7}, {0, 7}, {0, 7}, {1, 6}, {2, 5}}

func moonSprite(m MoonView) []string {
	rows := make([]string, 8)
	for y := 0; y < 8; y++ {
		xL, xR := moonDisc[y][0], moonDisc[y][1]
		w := xR - xL + 1
		lit := int(m.Illum*float64(w) + 0.5)
		if lit < 0 {
			lit = 0
		}
		if lit > w {
			lit = w
		}
		b := []byte("........")
		for k := 0; k < lit; k++ {
			x := xR - k
			if !m.Waxing {
				x = xL + k
			}
			b[x] = 'X'
		}
		rows[y] = string(b)
	}
	return rows
}

var sunHorizonIcon = []string{
	"........",
	"...X....",
	".X.X.X..",
	"..XXX...",
	".XXXXX..",
	"..XXX...",
	"XXXXXXXX",
	"........",
}

var (
	sunriseColor = RGB{0xFF, 0xC1, 0x4D}
	sunsetColor  = RGB{0xFF, 0x6A, 0x2A}
)

func SunPopupPayload(rising bool, label string, durationSec int) map[string]any {
	col := sunriseColor
	if !rising {
		col = sunsetColor
	}
	iconPx := bitmap8(sunHorizonIcon, col)
	return map[string]any{
		"text":        label,
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"stack":       false,
		"textColor":   hexOf(col),
		"draw":        []any{iconOp(iconPx)},
		"textCenter":  false,
		"textOffsetX": 9,
	}
}
