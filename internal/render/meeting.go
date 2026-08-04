package render

import (
	"fmt"
	"strings"
)

// Meeting render primitives: a rotating countdown tile ("ember-meet") and the
// T-minus popup. Both pair a drawn 8×8 calendar icon (cols 0–7) with native
// firmware text from col 9 (center:false + textOffset:9 — long titles scroll
// natively; same device-verified layout as the air popup). The chime is NOT
// built here — the caller attaches `soundRtttl` to the returned payload
// (awtrix-ng plays it in-band, draw/icon or not).

// meetingCalPage is the calendar body: solid border, sparse date dots inside.
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

// meetingCalRings is the binding-rings overlay painted in red over the top of
// the calendar page (rows 0–1, cols 1 and 6).
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

// meetingInk is the neutral light-gray used for the calendar body and text.
var meetingInk = RGB{0xE6, 0xE6, 0xE6}

// meetingRed is the ring colour — a muted red that reads as "calendar event"
// without burning the eye on an ambient display.
var meetingRed = RGB{0xCC, 0x33, 0x33}

// meetingIconPixels composes the 8×8 calendar icon (body + rings) into 64
// row-major 0xRRGGBB ints for use in a db [0,0,8,8] draw op.
func meetingIconPixels() []int {
	var f Frame
	paintBitmap(&f, 0, 0, meetingCalPage, meetingInk)
	paintBitmap(&f, 0, 0, meetingCalRings, meetingRed)
	return framePixelsRect(&f, 0, 0, 8, 8)
}

// MeetingPayload returns the rotating countdown tile payload for the
// "ember-meet" app slot: drawn calendar icon at cols 0–7 + native scrolling
// "<TITLE> <N>m" text from col 9. lifetime seconds controls how long AWTRIX
// keeps the slot alive before auto-expiring it.
func MeetingPayload(title string, minutes, lifetime int) map[string]any {
	return map[string]any{
		"text":        fmt.Sprintf("%s %dm", title, minutes),
		"textColor":   hexOf(meetingInk),
		"draw":        []any{bitmapOp(0, 0, 8, 8, meetingIconPixels())},
		"textCenter":  false,
		"textOffsetX": 9,
		"lifetimeMs":  msOf(lifetime),
		"durationMs":  msOf(rotateDwellSeconds),
	}
}

// MeetingPopupPayload returns the T-minus notification payload: drawn calendar
// icon at cols 0–7 + native scrolling "<TITLE> IN <N>M" text from col 9.
// wakeup:true wakes a sleeping display; stack:true lets simultaneous popups
// for DISTINCT meetings queue rather than replacing each other on-device
// (back-to-back meetings in the same tick each get their own notification).
// Same-occurrence deduplication is handled caller-side by the fired map, so
// stack:true does not cause duplicates for the same event.
// The chime is NOT built here — the caller attaches `soundRtttl` to the
// returned payload; awtrix-ng plays it in-band alongside the draw ops.
func MeetingPopupPayload(title string, leadMinutes, durationSec int) map[string]any {
	return map[string]any{
		"text":        fmt.Sprintf("%s IN %dM", title, leadMinutes),
		"textColor":   hexOf(meetingInk),
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"stack":       true,
		"draw":        []any{bitmapOp(0, 0, 8, 8, meetingIconPixels())},
		"textCenter":  false,
		"textOffsetX": 9,
	}
}

// MeetingTileFrame is the preview-only drawn frame (the canvas can't render
// native firmware text): icon + the same text in the 3×5 font, clipping at the
// right edge where the device would scroll. Mirrors ReminderPopupFrame.
func MeetingTileFrame(title string, minutes int) Frame {
	var f Frame
	paintBitmap(&f, 0, 0, meetingCalPage, meetingInk)
	paintBitmap(&f, 0, 0, meetingCalRings, meetingRed)
	drawDigits(&f, strings.ToUpper(fmt.Sprintf("%s %dM", title, minutes)), 9, 1, meetingInk)
	return f
}
