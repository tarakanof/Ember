package render

var usageIconClaude = []string{
	"..X..X..", ".XXXXXX.", ".X.XX.X.", "XX.XX.XX",
	"XXXXXXXX", ".X....X.", ".XXXXXX.", "........",
}

var usageIconCodex = []string{
	"X.......", ".X......", "..X.....", "...X....",
	"..X.....", ".X......", "X..XXXX.", "........",
}

// usageIconT3 is "T3" for T3 Code; the "3" (t3Three8) takes the state colour.
var usageIconT3 = []string{
	"........", "XXX.XXX.", ".X....X.", ".X...XX.",
	".X....X.", ".X..XXX.", "........", "........",
}

var (
	usageColorClaude = RGB{0xff, 0x7a, 0x18}
	usageColorCodex  = RGB{0x22, 0xd3, 0xee}
	usageGray        = RGB{0x6f, 0x77, 0x80}
	usageColon       = RGB{0x4d, 0x66, 0x78}
	usageTrack       = RGB{0x2c, 0x2c, 0x2c}
)

var (
	usageOK   = RGB{0x39, 0xd3, 0x53}
	usageWarn = RGB{0xe3, 0xa0, 0x08}
	usageHot  = RGB{0xf0, 0x4e, 0x4e}
)

func usageThreshold(pct int) RGB {
	switch {
	case pct < 70:
		return usageOK
	case pct < 90:
		return usageWarn
	default:
		return usageHot
	}
}

func dimThreshold(pct int) RGB {
	c := usageThreshold(pct)
	return RGB{uint8(int(c.R) * 55 / 100), uint8(int(c.G) * 55 / 100), uint8(int(c.B) * 55 / 100)}
}

func toInt(c RGB) int { return int(c.R)<<16 | int(c.G)<<8 | int(c.B) }

func usageBarPixels(pct int) []RGB {
	fill := (barW*pct + 50) / 100
	if pct > 0 && fill < 1 {
		fill = 1
	}
	out := make([]RGB, barW)
	for i := range out {
		if i < fill {
			out[i] = dimThreshold(pct)
		} else {
			out[i] = usageTrack
		}
	}
	return out
}

func bitmap8(icon []string, c RGB) []int {
	px := make([]int, 64)
	for y, row := range icon {
		for x, ch := range row {
			if ch == 'X' && x < 8 && y < 8 {
				px[y*8+x] = toInt(c)
			}
		}
	}
	return px
}

func drawClockInto(f *Frame, hhmm string, x int) {
	runes := []rune(hhmm)
	for i, ch := range runes {
		g := glyph(ch)
		col := colorWhite
		if ch == ':' {
			col = usageColon
		}
		if g != nil {
			paintBitmap(f, x, 1, g, col)
		}
		w := 3
		if ch == ':' {
			w = 1
		}
		kern := 1
		if ch == ':' || (i+1 < len(runes) && runes[i+1] == ':') {
			kern = 0
		}
		x += w + kern
	}
}

func drawBarInto(f *Frame, pct int) {
	for i, c := range usageBarPixels(pct) {
		paintCell(f, barX0+i, barRow, c)
	}
}

// LimitResetPopupPayload is the "5h limit reset — back to work" notification:
// drawn 8×8 tool icon + brand-coloured text, auto-dismiss.
func LimitResetPopupPayload(tool string, durationSec int) map[string]any {
	icon, color, label := usageIconClaude, usageColorClaude, "CLAUDE 5H RESET"
	if tool == "codex" {
		icon, color, label = usageIconCodex, usageColorCodex, "CODEX 5H RESET"
	}
	return map[string]any{
		"text":        label,
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"stack":       true,
		"textColor":   hexOf(color),
		"draw":        []any{iconOp(bitmap8(icon, color))},
		"textCenter":  false,
		"textOffsetX": 9,
	}
}
