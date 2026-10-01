package render

import (
	"fmt"
	"strings"
)

var meetingCalPage = []string{
	"........",
	"XXXXXXXX",
	"X......X",
	"X.X.X..X",
	"X......X",
	"X..X.X.X",
	"X......X",
	"XXXXXXXX",
}

var meetingCalRings = []string{
	".X....X.",
	".X....X.",
	"........",
	"........",
	"........",
	"........",
	"........",
	"........",
}

var meetingInk = RGB{0xE6, 0xE6, 0xE6}

var meetingRed = RGB{0xCC, 0x33, 0x33}

func meetingIconPixels() []int {
	var f Frame
	paintBitmap(&f, 0, 0, meetingCalPage, meetingInk)
	paintBitmap(&f, 0, 0, meetingCalRings, meetingRed)
	return framePixelsRect(&f, 0, 0, 8, 8)
}

// MeetingPayload returns the rotating countdown tile payload for the
// "ember-meet" app slot: drawn calendar icon at cols 0–7 + native "<N>M
// <TITLE>" text from col 9.
func MeetingPayload(title string, minutes, lifetime int) map[string]any {
	return pinText(map[string]any{
		"text":        meetingTileText(title, minutes),
		"textColor":   hexOf(meetingInk),
		"draw":        []any{iconOp(meetingIconPixels())},
		"textCenter":  false,
		"textOffsetX": 9,
		"lifetimeMs":  msOf(lifetime),
		"durationMs":  msOf(rotateDwellSeconds),
	})
}

func meetingTileText(title string, minutes int) string {
	return fmt.Sprintf("%dM %s", minutes, title)
}

// MeetingPopupPayload returns the T-minus notification payload: drawn calendar
// icon at cols 0–7 + native scrolling "<TITLE> IN <N>M" text from col 9.
func MeetingPopupPayload(title string, leadMinutes, durationSec int) map[string]any {
	return readOnce(pinText(map[string]any{
		"text":        fmt.Sprintf("%s IN %dM", title, leadMinutes),
		"textColor":   hexOf(meetingInk),
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"stack":       true,
		"draw":        []any{iconOp(meetingIconPixels())},
		"textCenter":  false,
		"textOffsetX": 9,
	}))
}

// MeetingTileFrame is the preview-only drawn frame (the canvas can't render
// native firmware text): icon + the same text in the 3×5 font, clipping at the
// right edge where the device would scroll.
func MeetingTileFrame(title string, minutes int) Frame {
	var f Frame
	paintBitmap(&f, 0, 0, meetingCalPage, meetingInk)
	paintBitmap(&f, 0, 0, meetingCalRings, meetingRed)
	drawDigits(&f, strings.ToUpper(meetingTileText(title, minutes)), contentX, textRow, meetingInk)
	return f
}
