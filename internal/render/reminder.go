package render

import "strings"

// Reminder widget render primitives. A reminder is an alarm popup (a notification
// — there is no rotating tile): a drawn bell icon + the reminder text, optionally
// with a chime. Mirrors the weather popup shape.

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

// reminderGold is the bell colour (a warm amber that reads as "alarm").
var reminderGold = RGB{0xff, 0xcc, 0x33}

// ReminderPopupPayload returns a notification payload for an alarm: a drawn bell
// icon at cols 0–7 + the reminder text scrolling from col 9. iconID, when
// non-empty, swaps in a native AWTRIX icon (firmware then owns the layout, so we
// drop center/textOffset). stack:true so two reminders due at the same minute
// queue rather than the second replacing the first on-device. hold:true makes the
// alarm take over the display until the user dismisses it (middle button) instead
// of auto-dismissing after durationSec — the alarm then ignores the duration.
//
// The chime is NOT carried here: on AWTRIX 0.98 a notification's `sound`/`rtttl`
// is silently dropped whenever the notification also has a `draw` or `icon`, so
// the caller plays it separately via the device's /api/rtttl endpoint.
// ReminderPopupFrame composes the drawn alarm-popup frame for settings
// previews: the gold bell at cols 0-7 + the text from col 9 in the 3×5 font
// (uppercased — the font has no lowercase; overlong text clips at the right
// edge, where the device would scroll instead). The device payload keeps its
// own firmware text layout — this frame is preview-only.
func ReminderPopupFrame(text string) Frame {
	var f Frame
	paintBitmap(&f, 0, 0, reminderBell, reminderGold)
	drawDigits(&f, strings.ToUpper(text), 9, 1, reminderGold)
	return f
}

func ReminderPopupPayload(text, iconID string, durationSec int, hold bool) map[string]any {
	p := map[string]any{
		"text":     text,
		"duration": durationSec,
		"wakeup":   true,
		"stack":    true,
		"hold":     hold,
		"color":    hexOf(reminderGold),
	}
	if iconID != "" {
		p["icon"] = iconID
	} else {
		p["draw"] = []any{map[string]any{"db": []any{0, 0, 8, 8, bitmap8(reminderBell, reminderGold)}}}
		p["center"] = false
		p["textOffset"] = 9
	}
	return p
}
