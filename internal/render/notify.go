package render

// NotifyPayload builds the notification for POST /v1/notify: the caller's
// text in color (a "#RRGGBB" string), shown for durationSec, or until
// dismissed when hold is true. It wakes a sleeping panel and replaces the
// notification on screen rather than queueing behind it. textCase is the
// caller's NG textCase; "" pins "upper", like every other Ember text payload.
// Callers validate it with ValidTextCase. The caller sets the notification's
// name.
func NotifyPayload(text, color, textCase string, durationSec int, hold bool) map[string]any {
	p := pinText(map[string]any{
		"text":       text,
		"textColor":  color,
		"durationMs": msOf(durationSec),
		"hold":       hold,
		"wakeup":     true,
		"stack":      false,
	})
	if textCase != "" {
		p["textCase"] = textCase
	}
	return p
}

// ValidTextCase reports whether s is one of NG's textCase values (inherit,
// upper, asTyped); anything else would 422 the push.
func ValidTextCase(s string) bool { return ngEnums["textCase"][s] }
