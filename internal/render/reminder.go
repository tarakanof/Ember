package render

import "strings"

var reminderBell = []string{
	"...XX...",
	"..XXXX..",
	"..XXXX..",
	".XXXXXX.",
	".XXXXXX.",
	"XXXXXXXX",
	"...XX...",
	"........",
}

var reminderGold = RGB{0xff, 0xcc, 0x33}

// ReminderPopupFrame composes the preview-only drawn alarm popup: gold bell
// plus the uppercased text in the 3×5 font.
func ReminderPopupFrame(text string) Frame {
	var f Frame
	paintBitmap(&f, 0, 0, reminderBell, reminderGold)
	drawDigits(&f, strings.ToUpper(text), 9, 1, reminderGold)
	return f
}

// ReminderPopupPayload returns the alarm notification payload: a drawn bell
// icon at cols 0–7 plus the reminder text scrolling from col 9.
func ReminderPopupPayload(text, iconID string, durationSec int, hold bool) map[string]any {
	p := pinText(map[string]any{
		"text":       text,
		"durationMs": msOf(durationSec),
		"wakeup":     true,
		"stack":      true,
		"hold":       hold,
		"textColor":  hexOf(reminderGold),
	})
	if !hold {
		readOnce(p)
	}
	if iconID != "" {
		p["icon"] = iconID
	} else {
		p["draw"] = []any{iconOp(bitmap8(reminderBell, reminderGold))}
		p["textCenter"] = false
		p["textOffsetX"] = 9
	}
	return p
}
