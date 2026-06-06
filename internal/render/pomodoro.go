package render

import "fmt"

// PomodoroView is the render input for one Pomodoro frame. Phase is the engine
// phase string ("focus" | "short_break" | "long_break"). A zero-value
// FocusColor/BreakColor (black) falls back to the built-in default.
type PomodoroView struct {
	Phase        string
	Paused       bool
	RemainingSec int
	PlannedSec   int
	FocusColor   RGB
	BreakColor   RGB
}

// Pomodoro phase strings (mirror internal/pomodoro.Phase; kept as plain strings
// so the render package stays decoupled from the engine).
const (
	pomoFocus = "focus"
	pomoShort = "short_break"
	pomoLong  = "long_break"
)

var (
	pomoFocusDefault = RGB{0xff, 0x00, 0x00} // pure red
	pomoShortDefault = RGB{0x2e, 0xe8, 0x5e} // green
	pomoLongDefault  = RGB{0x4f, 0xa9, 0xff} // blue
	pomoTrack        = RGB{0x22, 0x22, 0x22} // dim progress track
	pomoStem         = RGB{0x3c, 0xb0, 0x43} // tomato leaves/stem
	pomoCupGray      = RGB{0xb4, 0xb4, 0xb4} // coffee-mug body (neutral gray)
	pomoSteam        = RGB{0x55, 0x55, 0x55} // dim steam wisp above the mug
)

// tomatoBody is the focus pictogram body (rows 2..6), painted in the phase
// colour; the stem (rows 0..1) is painted green on top.
var tomatoBody = []string{
	".......", // row0 (stem painted separately)
	".......", // row1
	".XXXXX.", // row2
	"XXXXXXX", // row3
	"XXXXXXX", // row4
	"XXXXXXX", // row5
	".XXXXX.", // row6
}

var tomatoStem = []string{
	"...X...", // row0
	"..XXX..", // row1
}

// coffeeMug is the short-break pictogram: a gray mug with a handle. The steam
// wisp above is painted separately (dimmer) so it reads as a hot coffee.
var coffeeMug = []string{
	".......", // row0 (steam painted separately)
	".......", // row1
	".XXXXX.", // row2 rim
	".X...XX", // row3 body + handle
	".X...X.", // row4 body + handle
	".X...XX", // row5 body + handle
	".XXXXX.", // row6 base
}

var coffeeSteam = []string{
	"..X.X..", // row0
	"..X.X..", // row1
}

// breakMoon is the long-break pictogram (a crescent — "rest").
var breakMoon = []string{
	"..XXX..",
	".XX....",
	"XX.....",
	"XX.....",
	"XX.....",
	".XX....",
	"..XXX..",
}

// HexRGB parses a "#RRGGBB" colour string into an RGB. ok is false on malformed
// input. Exported for callers (the service) that hold colours as config strings.
func HexRGB(s string) (RGB, bool) { return parseHex(s) }

func isZeroRGB(c RGB) bool { return c == RGB{} }

func dimRGB(c RGB) RGB { return RGB{c.R / 2, c.G / 2, c.B / 2} }

// pomoBaseColor returns the un-dimmed phase colour, honouring overrides.
func pomoBaseColor(v PomodoroView) RGB {
	switch v.Phase {
	case pomoFocus:
		if isZeroRGB(v.FocusColor) {
			return pomoFocusDefault
		}
		return v.FocusColor
	case pomoShort:
		if isZeroRGB(v.BreakColor) {
			return pomoShortDefault
		}
		return v.BreakColor
	case pomoLong:
		if isZeroRGB(v.BreakColor) {
			return pomoLongDefault
		}
		return v.BreakColor
	default:
		return colorWhite
	}
}

// drawColon paints the MM:SS separator as two dots relative to the digit origin.
func drawColon(f *Frame, x, startY int, c RGB) {
	paintCell(f, x, startY+1, c)
	paintCell(f, x, startY+3, c)
}

// progressWidth returns how many of the 32 columns the remaining-time bar fills.
func progressWidth(remaining, planned int) int {
	if planned <= 0 {
		return 0
	}
	w := (32*remaining + planned/2) / planned
	if w < 0 {
		return 0
	}
	if w > 32 {
		return 32
	}
	return w
}

// RenderPomodoro paints a graphics-first Pomodoro frame: a phase pictogram in
// the left columns, the MM:SS countdown in the phase colour, and a
// remaining-time progress bar on the bottom row. Paused dims the colour.
func RenderPomodoro(v PomodoroView) *Frame {
	f := &Frame{}
	c := pomoBaseColor(v)
	if v.Paused {
		c = dimRGB(c)
	}

	// Pictogram (cols 0..6, rows 0..6).
	switch v.Phase {
	case pomoFocus:
		paintBitmap(f, 0, 0, tomatoBody, c)
		paintBitmap(f, 0, 0, tomatoStem, pomoStem)
	case pomoShort:
		// The mug is a fixed neutral gray (the requested "gray coffee cup"); the
		// countdown + progress bar still carry the break colour for the phase cue.
		paintBitmap(f, 0, 0, coffeeSteam, pomoSteam)
		paintBitmap(f, 0, 0, coffeeMug, pomoCupGray)
	case pomoLong:
		paintBitmap(f, 0, 0, breakMoon, c)
	}

	// MM:SS countdown (digits via the shared 3×5 font), origin at (9,1).
	rem := v.RemainingSec
	if rem < 0 {
		rem = 0
	}
	mm := rem / 60
	ss := rem % 60
	if mm > 99 {
		mm = 99
	}
	const startX, startY = 9, 1
	drawDigits(f, fmt.Sprintf("%02d", mm), startX, startY, c)
	drawColon(f, startX+8, startY, c)
	drawDigits(f, fmt.Sprintf("%02d", ss), startX+10, startY, c)

	// Progress bar on row 7: dim track under the whole width, phase colour
	// for the remaining portion.
	paintRow(f, 0, 31, 7, pomoTrack)
	if w := progressWidth(rem, v.PlannedSec); w > 0 {
		paintRow(f, 0, w-1, 7, c)
	}

	return f
}

// pomoIconID maps a phase to an on-device AWTRIX icon (in /ICONS): focus →
// tomato (29802), breaks → coffee (6396).
func pomoIconID(phase string) string {
	if phase == pomoFocus {
		return "29802"
	}
	return "6396"
}

// pomoProgressPct returns the remaining-time fill as a 0..100 percentage for the
// native AWTRIX progress bar.
func pomoProgressPct(remaining, planned int) int {
	if planned <= 0 {
		return 0
	}
	p := (100*remaining + planned/2) / planned
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// PomodoroPayload encodes a Pomodoro frame using AWTRIX's built-in animated icon
// (tomato for focus, coffee for breaks) + a native MM:SS countdown + the native
// progress bar. Paused dims the phase colour (the animated icon keeps animating
// — an accepted cosmetic). RenderPomodoro (the drawn variant) is retained for
// tests / any future preview use.
func PomodoroPayload(v PomodoroView, lifetimeSeconds int) map[string]any {
	c := pomoBaseColor(v)
	if v.Paused {
		c = dimRGB(c)
	}
	hex := fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	rem := v.RemainingSec
	if rem < 0 {
		rem = 0
	}
	mm := rem / 60
	if mm > 99 {
		mm = 99
	}
	ss := rem % 60
	// NOTE: with the built-in `icon` field set, AWTRIX already reserves the
	// left 8px and centres `text` in the remaining region — so we must NOT set
	// `textOffset`/`center` here (doing so double-shifts the text right and
	// clips the last digit, e.g. "23:45" → "23:4"). `noScroll` keeps the static
	// MM:SS from scrolling. (This differs from the usage-widget frames, which
	// draw the icon as a `db` and DO need center:false + textOffset.)
	trackHex := fmt.Sprintf("#%02X%02X%02X", pomoTrack.R, pomoTrack.G, pomoTrack.B)
	return map[string]any{
		"icon":       pomoIconID(v.Phase),
		"text":       fmt.Sprintf("%02d:%02d", mm, ss),
		"color":      hex,
		"noScroll":   true,
		"progress":   pomoProgressPct(rem, v.PlannedSec),
		"progressC":  hex,
		"progressBC": trackHex, // dim track (else AWTRIX defaults to a white track)
		"lifetime":   lifetimeSeconds,
		"duration":   lifetimeSeconds,
		"prio":       true,
		"force":      true,
	}
}
