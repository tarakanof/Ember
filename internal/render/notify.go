package render

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

func ValidTextCase(s string) bool { return ngEnums["textCase"][s] }
