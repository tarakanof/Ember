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

// Exported accessors so cmd/ember can build usage apps without re-declaring the
// sprites/colours (they stay package-private, single source of truth).
func UsageIconClaude() []string { return usageIconClaude }
func UsageIconCodex() []string  { return usageIconCodex }
func UsageColorClaude() RGB     { return usageColorClaude }
func UsageColorCodex() RGB      { return usageColorCodex }

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

// unitBitmap renders two 3-wide glyphs side-by-side (a at local x0, b at x3)
// into a 6x5 = 30 row-major int array (for a db [26,1,6,5] draw op). Each glyph
// gets its own colour so the unit can be tool+gray (5h/7d) or gray+gray
// (per-model OP/SO markers, which have no leading digit).
func unitBitmap(a, b rune, aColor, bColor RGB) []int {
	px := make([]int, 6*5)
	put := func(g []string, x0 int, c RGB) {
		if g == nil {
			return
		}
		for y, row := range g {
			for x, ch := range row {
				col := x0 + x
				if ch == 'X' && col >= 0 && col < 6 && y < 5 {
					px[y*6+col] = toInt(c)
				}
			}
		}
	}
	put(glyph(a), 0, aColor)
	put(glyph(b), 3, bColor)
	return px
}

// drawUnitInto paints the flush-right unit (digit in tool colour + gray letter)
// at cols 26-31, rows 1-5, into a Frame (used by the fully-drawn 5h frame).
func drawUnitInto(f *Frame, digit, letter rune, tool RGB) {
	if g := glyph(digit); g != nil {
		paintBitmap(f, 26, 1, g, tool)
	}
	if g := glyph(letter); g != nil {
		paintBitmap(f, 29, 1, g, usageGray)
	}
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

// UsageFiveHourPayload returns a fully-drawn 5h frame (icon + tight clock +
// unit + bar) as a single full-frame db op. lifetime is in seconds.
func UsageFiveHourPayload(icon []string, tool RGB, hhmm string, pct int, lifetime int) map[string]any {
	var f Frame
	paintBitmap(&f, 0, 0, icon, tool)
	drawClockInto(&f, hhmm, 9)
	drawUnitInto(&f, '5', 'h', tool)
	drawBarInto(&f, pct)
	return map[string]any{
		"draw":     []any{map[string]any{"db": []any{0, 0, 32, 8, framePixels(&f)}}},
		"lifetime": lifetime, "duration": 6,
	}
}

// usageWeeklyPayload is the shared weekly/per-model builder: native-font day
// name + drawn icon/unit/bar (3 db ops). uA/uB colour the two unit glyphs.
func usageWeeklyPayload(icon []string, tool RGB, day string, pct int, uDigit, uLetter rune, lifetime int, uA, uB RGB) map[string]any {
	iconPx := bitmap8(icon, tool)
	unitPx := unitBitmap(uDigit, uLetter, uA, uB)
	barPx := make([]any, 24)
	for i, c := range usageBarPixels(pct) {
		barPx[i] = toInt(c)
	}
	return map[string]any{
		"draw": []any{
			map[string]any{"db": []any{0, 0, 8, 8, iconPx}},
			map[string]any{"db": []any{26, 1, 6, 5, unitPx}},
			map[string]any{"db": []any{8, 7, 24, 1, barPx}},
		},
		"text": day, "center": false, "textOffset": 9, "noScroll": true,
		"color": "FFFFFF", "lifetime": lifetime, "duration": 6,
	}
}

// UsageWeeklyPayload: native-font day name + drawn icon/unit/bar. The unit digit
// is tool-coloured, the unit letter gray.
func UsageWeeklyPayload(icon []string, tool RGB, day string, pct int, uDigit, uLetter rune, lifetime int) map[string]any {
	return usageWeeklyPayload(icon, tool, day, pct, uDigit, uLetter, lifetime, tool, usageGray)
}

// UsageModelPayload: per-model weekly frame. The unit slot shows the model
// marker (opus -> "OP", sonnet -> "SO"), both glyphs gray (no leading digit).
func UsageModelPayload(icon []string, tool RGB, day, model string, pct, lifetime int) map[string]any {
	d, l := 'O', 'P'
	if model == "sonnet" {
		d, l = 'S', 'O'
	}
	return usageWeeklyPayload(icon, tool, day, pct, d, l, lifetime, usageGray, usageGray)
}

// LimitResetPopupPayload is the "5h limit reset — back to work" notification:
// drawn 8×8 tool icon + brand-coloured text, auto-dismiss. The chime is NOT in
// the payload (fw 0.98 drops a notification's sound when it draws) — the
// caller plays it via PlayRTTTL, same as reminders.
func LimitResetPopupPayload(tool string, durationSec int) map[string]any {
	icon, color, label := usageIconClaude, usageColorClaude, "CLAUDE 5H RESET"
	if tool == "codex" {
		icon, color, label = usageIconCodex, usageColorCodex, "CODEX 5H RESET"
	}
	return map[string]any{
		"text":       label,
		"duration":   durationSec,
		"wakeup":     true,
		"stack":      true,
		"color":      hexOf(color),
		"draw":       []any{map[string]any{"db": []any{0, 0, 8, 8, bitmap8(icon, color)}}},
		"center":     false,
		"textOffset": 9,
	}
}
