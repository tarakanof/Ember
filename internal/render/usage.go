package render

// 8x8 tool icons ('X' lit, painted in the tool colour). From the approved spec.
var usageIconClaude = []string{
	"..X..X..", ".XXXXXX.", ".X.XX.X.", "XX.XX.XX",
	"XXXXXXXX", ".X....X.", ".XXXXXX.", "........",
}

var usageIconCodex = []string{
	"X.......", ".X......", "..X.....", "...X....",
	"..X.....", ".X......", "X..XXXX.", "........",
}

var (
	usageColorClaude = RGB{0xff, 0x7a, 0x18}
	usageColorCodex  = RGB{0x22, 0xd3, 0xee}
	usageGray        = RGB{0x6f, 0x77, 0x80}
	usageColon       = RGB{0x4d, 0x66, 0x78}
	usageTrack       = RGB{0x2c, 0x2c, 0x2c}
)

func usageThreshold(pct int) RGB { // green/amber/red
	switch {
	case pct < 70:
		return RGB{0x39, 0xd3, 0x53}
	case pct < 90:
		return RGB{0xe3, 0xa0, 0x08}
	default:
		return RGB{0xf0, 0x4e, 0x4e}
	}
}

func dimThreshold(pct int) RGB { // ~55% of the threshold colour
	c := usageThreshold(pct)
	return RGB{uint8(int(c.R) * 55 / 100), uint8(int(c.G) * 55 / 100), uint8(int(c.B) * 55 / 100)}
}

func toInt(c RGB) int { return int(c.R)<<16 | int(c.G)<<8 | int(c.B) }

// usageBarPixels returns 24 colours (content cols 8-31) for a dimmed 1px bar.
func usageBarPixels(pct int) []RGB {
	fill := (24*pct + 50) / 100
	if pct > 0 && fill < 1 {
		fill = 1
	}
	out := make([]RGB, 24)
	for i := range out {
		if i < fill {
			out[i] = dimThreshold(pct)
		} else {
			out[i] = usageTrack
		}
	}
	return out
}

// bitmap8 renders an 8x8 icon sprite to 64 row-major ints in colour c (for a
// db [0,0,8,8] draw op).
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

// drawClockInto paints HH:MM left-aligned at x, rows 1-5, with a tight, dimmed
// colon (0-px kerning around ':'). Uses per-glyph advance (NOT drawDigits) so
// the 1-wide ':' and 3-wide digits pack tightly.
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

// drawBarInto paints the 1px dimmed content-area bar at row 7, cols 8-31.
func drawBarInto(f *Frame, pct int) {
	for i, c := range usageBarPixels(pct) {
		paintCell(f, 8+i, 7, c)
	}
}

// LimitResetPopupPayload is the "5h limit reset — back to work" notification:
// drawn 8×8 tool icon + brand-coloured text, auto-dismiss. The caller adds the
// notification's name and its soundRtttl chime — awtrix-ng plays a
// notification's melody alongside its draw commands.
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
		"draw":        []any{bitmapOp(0, 0, 8, 8, bitmap8(icon, color))},
		"textCenter":  false,
		"textOffsetX": 9,
	}
}
