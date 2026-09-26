package render

// NotifyPayload builds the notification for POST /v1/notify: the caller's
// text in color (a "#RRGGBB" string), shown for durationSec, or until
// dismissed when hold is true. It wakes a sleeping panel and replaces the
// notification on screen rather than queueing behind it. The caller sets the
// notification's name.
func NotifyPayload(text, color string, durationSec int, hold bool) map[string]any {
	return pinText(map[string]any{
		"text":       text,
		"textColor":  color,
		"durationMs": msOf(durationSec),
		"hold":       hold,
		"wakeup":     true,
		"stack":      false,
	})
}
