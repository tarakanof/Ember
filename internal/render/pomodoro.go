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
	pomoFocusDefault = RGB{0xff, 0x3b, 0x30} // red
	pomoShortDefault = RGB{0x2e, 0xe8, 0x5e} // green
	pomoLongDefault  = RGB{0x4f, 0xa9, 0xff} // blue
	pomoTrack        = RGB{0x22, 0x22, 0x22} // dim progress track
	pomoStem         = RGB{0x3c, 0xb0, 0x43} // tomato leaves/stem
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

// breakCup is the short-break pictogram (a mug with a handle).
var breakCup = []string{
	".......",
	".XXXXX.",
	".X...XX",
	".X...X.",
	".X...XX",
	".XXXXX.",
	"..XXX..",
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
		paintBitmap(f, 0, 0, breakCup, c)
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

// PomodoroPayload encodes a Pomodoro frame as an AWTRIX CustomApp payload
// (single-frame draw, per firmware 0.98 constraints).
func PomodoroPayload(v PomodoroView, lifetimeSeconds int) map[string]any {
	return frameToCustomApp(RenderPomodoro(v), lifetimeSeconds)
}
