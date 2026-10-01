package render

import "fmt"

// PomodoroView is the render input for one Pomodoro frame.
type PomodoroView struct {
	Phase        string
	Paused       bool
	RemainingSec int
	PlannedSec   int
	FocusColor   RGB
	BreakColor   RGB
}

const (
	pomoFocus = "focus"
	pomoShort = "short_break"
	pomoLong  = "long_break"
)

var (
	pomoFocusDefault = RGB{0xff, 0x00, 0x00}
	pomoShortDefault = RGB{0x00, 0xff, 0x00}
	pomoLongDefault  = RGB{0x4f, 0xa9, 0xff}
	pomoTrack        = RGB{0x22, 0x22, 0x22}
	pomoStem         = RGB{0x3c, 0xb0, 0x43}
	pomoCupGray      = RGB{0xb4, 0xb4, 0xb4}
	pomoSteam        = RGB{0x55, 0x55, 0x55}
)

var tomatoBody = []string{
	".......",
	".......",
	".XXXXX.",
	"XXXXXXX",
	"XXXXXXX",
	"XXXXXXX",
	".XXXXX.",
}

var tomatoStem = []string{
	"...X...",
	"..XXX..",
}

var coffeeMug = []string{
	".......",
	".......",
	".XXXXX.",
	".X...XX",
	".X...X.",
	".X...XX",
	".XXXXX.",
}

var coffeeSteam = []string{
	"..X.X..",
	"..X.X..",
}

// HexRGB parses a "#RRGGBB" colour string into an RGB.
func HexRGB(s string) (RGB, bool) { return parseHex(s) }

func isZeroRGB(c RGB) bool { return c == RGB{} }

func dimRGB(c RGB) RGB { return RGB{c.R / 2, c.G / 2, c.B / 2} }

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

func drawColon(f *Frame, x, startY int, c RGB) {
	paintCell(f, x, startY+1, c)
	paintCell(f, x, startY+3, c)
}

func progressWidth(remaining, planned int) int {
	if planned <= 0 {
		return 0
	}
	w := (barW*remaining + planned/2) / planned
	if w < 0 {
		return 0
	}
	if w > barW {
		return barW
	}
	return w
}

const pomoTimeW = 17

const pomoTimeX = contentX + (contentW-pomoTimeW)/2

// RenderPomodoro paints the drawn preview of the Pomodoro tile for
// /v1/pomodoro/preview.
func RenderPomodoro(v PomodoroView) *Frame {
	f := &Frame{}
	c := pomoBaseColor(v)
	if v.Paused {
		c = dimRGB(c)
	}

	switch v.Phase {
	case pomoFocus:
		paintBitmap(f, 0, 0, tomatoBody, c)
		paintBitmap(f, 0, 0, tomatoStem, pomoStem)
	case pomoShort, pomoLong:
		paintBitmap(f, 0, 0, coffeeSteam, pomoSteam)
		paintBitmap(f, 0, 0, coffeeMug, pomoCupGray)
	}

	rem := v.RemainingSec
	if rem < 0 {
		rem = 0
	}
	mm := rem / 60
	ss := rem % 60
	if mm > 99 {
		mm = 99
	}
	drawDigits(f, fmt.Sprintf("%02d", mm), pomoTimeX, textRow, c)
	drawColon(f, pomoTimeX+8, textRow, c)
	drawDigits(f, fmt.Sprintf("%02d", ss), pomoTimeX+10, textRow, c)

	paintRow(f, barX0, panelW-1, barRow, pomoTrack)
	if w := progressWidth(rem, v.PlannedSec); w > 0 {
		paintRow(f, barX0, barX0+w-1, barRow, c)
	}

	return f
}

// Native AWTRIX icon IDs (in /ICONS) that Pomodoro payloads reference: tomato
// for focus, coffee for breaks.
const (
	PomoFocusIconID = "29802"
	PomoBreakIconID = "6396"
)

func pomoIconID(phase string) string {
	if phase == pomoFocus {
		return PomoFocusIconID
	}
	return PomoBreakIconID
}

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

// PomodoroPayload encodes a Pomodoro frame using AWTRIX's built-in animated
// icon (tomato for focus, coffee for breaks) + a native MM:SS countdown + the
// native progress bar.
func PomodoroPayload(v PomodoroView, lifetimeSeconds int) map[string]any {
	c := pomoBaseColor(v)
	if v.Paused {
		c = dimRGB(c)
	}
	hex := hexOf(c)
	rem := v.RemainingSec
	if rem < 0 {
		rem = 0
	}
	mm := rem / 60
	if mm > 99 {
		mm = 99
	}
	ss := rem % 60
	p := map[string]any{
		"icon":               pomoIconID(v.Phase),
		"text":               fmt.Sprintf("%02d:%02d", mm, ss),
		"textColor":          hex,
		"scroll":             scrollStatic(),
		"progress":           pomoProgressPct(rem, v.PlannedSec),
		"progressColor":      hex,
		"progressTrackColor": hexOf(pomoTrack),
		"lifetimeMs":         msOf(lifetimeSeconds),
	}
	if v.Paused {
		p["textFadeMs"] = pomoPausedFadeMs
	}
	applyHold(p, lifetimeSeconds)
	return p
}

const pomoPausedFadeMs = 2000
