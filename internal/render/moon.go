package render

// Moon-phase icon for the weather tile (clear nights) and the sunrise/sunset
// popup. Phase/illumination is computed by the caller (cmd/ember); this layer
// only turns it into pixels.

// MoonView is the moon's appearance: Illum is the illuminated fraction (0 = new,
// 1 = full); Waxing lights the right limb (growing), waning the left.
type MoonView struct {
	Illum  float64
	Waxing bool
}

// moonColor is a soft pale moonlight.
var moonColor = RGB{0xE8, 0xE6, 0xC0}

// moonDisc gives the lit column span [xL, xR] of the 8×8 moon disc per row.
var moonDisc = [8][2]int{{2, 5}, {1, 6}, {0, 7}, {0, 7}, {0, 7}, {0, 7}, {1, 6}, {2, 5}}

// moonSprite renders the illuminated portion of the moon as an 8×8 'X'/'.'
// sprite. Per row, the lit run is the rightmost (waxing) or leftmost (waning)
// fraction of the disc's width — a readable crescent→gibbous at 8px. A new moon
// (Illum≈0) is blank; a full moon is the whole disc.
func moonSprite(m MoonView) []string {
	rows := make([]string, 8)
	for y := 0; y < 8; y++ {
		xL, xR := moonDisc[y][0], moonDisc[y][1]
		w := xR - xL + 1
		lit := int(m.Illum*float64(w) + 0.5)
		if lit < 0 {
			lit = 0
		}
		if lit > w {
			lit = w
		}
		b := []byte("........")
		for k := 0; k < lit; k++ {
			x := xR - k // waxing: fill from the right limb
			if !m.Waxing {
				x = xL + k // waning: fill from the left limb
			}
			b[x] = 'X'
		}
		rows[y] = string(b)
	}
	return rows
}

// sunHorizonIcon is a sun sitting on the horizon, used for both sunrise and
// sunset popups (the colour distinguishes them).
var sunHorizonIcon = []string{
	"........",
	"...X....",
	".X.X.X..",
	"..XXX...",
	".XXXXX..",
	"..XXX...",
	"XXXXXXXX",
	"........",
}

var (
	sunriseColor = RGB{0xFF, 0xC1, 0x4D} // warm amber
	sunsetColor  = RGB{0xFF, 0x6A, 0x2A} // orange
)

// SunPopupPayload renders a sunrise/sunset notification: an 8×8 sun-on-horizon
// glyph (cols 0–7) + a left-aligned label like "SUNRISE 5:21". `rising` tints it
// amber (sunrise) or orange (sunset). The caller owns wakeup/stack/duration; no
// sound is attached here (add via /api/rtttl like severe alerts if wanted).
func SunPopupPayload(rising bool, label string, durationSec int) map[string]any {
	col := sunriseColor
	if !rising {
		col = sunsetColor
	}
	iconPx := bitmap8(sunHorizonIcon, col)
	return map[string]any{
		"text":       label,
		"duration":   durationSec,
		"wakeup":     true,
		"stack":      false,
		"color":      hexOf(col),
		"draw":       []any{map[string]any{"db": []any{0, 0, 8, 8, iconPx}}},
		"center":     false,
		"textOffset": 9,
	}
}
