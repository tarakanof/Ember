package render

import "fmt"

const ngMaxPayloadBytes = 8192

func msOf(seconds int) int { return seconds * 1000 }

func bitmapOp(x, y, w, h int, data []int) []any {
	return []any{"bitmap", x, y, w, h, data}
}

func scrollStatic() map[string]any {
	return map[string]any{"mode": "static"}
}

func scrollStaticWhenFits() map[string]any {
	return map[string]any{"whenFits": "static"}
}

const (
	OverlayRain    = "rain"
	OverlayDrizzle = "drizzle"
	OverlaySnow    = "snow"
	OverlayStorm   = "storm"
	OverlayThunder = "thunder"
	OverlayFrost   = "frost"
)

func WithOverlay(p map[string]any, name string) map[string]any {
	if name != "" {
		p["overlay"] = name
	}
	return p
}

func pinText(p map[string]any) map[string]any {
	p["textCase"] = "upper"
	p["scroll"] = scrollStaticWhenFits()
	return p
}

func readOnce(p map[string]any) map[string]any {
	p["repeat"] = 1
	return p
}

func applyHold(p map[string]any, lifetimeSeconds int) {
	p["durationMs"] = msOf(lifetimeSeconds)
}

func hexOf(c RGB) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }
