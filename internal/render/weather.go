package render

import "fmt"

// Weather widget render primitives. The weather app renders as its own AWTRIX
// custom app (a rotating tile) and as ad-hoc notification popups, mirroring the
// usage-widget language: an 8×8 condition icon at cols 0–7 + a temperature
// readout. Icons are *drawn* (not native AWTRIX icon IDs) so the tile is
// deterministic on any device; the popup can optionally swap in a native
// animated icon by ID (configured server-side).

// Weather condition keys. The provider-specific code mapping lives in
// cmd/ember; render only knows these six visual buckets.
const (
	WeatherClear  = "clear"
	WeatherClouds = "clouds"
	WeatherFog    = "fog"
	WeatherRain   = "rain"
	WeatherSnow   = "snow"
	WeatherStorm  = "storm"
)

// 8×8 condition icons ('X' lit, painted in the condition colour).
var weatherIconClear = []string{
	"...X....",
	"X.....X.",
	"..XXX...",
	".XXXXX..",
	".XXXXX..",
	"..XXX...",
	"X.....X.",
	"...X....",
}

var weatherIconClouds = []string{
	"........",
	"........",
	"...XXX..",
	"..XXXXX.",
	".XXXXXXX",
	".XXXXXXX",
	"........",
	"........",
}

var weatherIconFog = []string{
	"........",
	".XXXXXX.",
	"........",
	"XXXXXXXX",
	"........",
	".XXXXXX.",
	"........",
	"XXXXXXXX",
}

var weatherIconRain = []string{
	"........",
	"..XXX...",
	".XXXXX..",
	".XXXXX..",
	"........",
	".X.X.X..",
	"X.X.X...",
	"........",
}

var weatherIconSnow = []string{
	"........",
	"..XXX...",
	".XXXXX..",
	".XXXXX..",
	"........",
	".X.X.X..",
	"..X.X...",
	".X.X.X..",
}

var weatherIconStorm = []string{
	"........",
	"..XXX...",
	".XXXXX..",
	".XXXXX..",
	"...X....",
	"..XX....",
	"...X....",
	"..X.....",
}

var (
	weatherColClear  = RGB{0xff, 0xc1, 0x4d} // warm amber
	weatherColClouds = RGB{0x9a, 0xa3, 0xad} // light gray
	weatherColFog    = RGB{0x80, 0x88, 0x90} // dim gray
	weatherColRain   = RGB{0x4f, 0xa9, 0xff} // blue
	weatherColSnow   = RGB{0xe6, 0xf0, 0xff} // near-white
	weatherColStorm  = RGB{0xb1, 0x6c, 0xff} // purple
)

func weatherIcon(cond string) []string {
	switch cond {
	case WeatherClear:
		return weatherIconClear
	case WeatherFog:
		return weatherIconFog
	case WeatherRain:
		return weatherIconRain
	case WeatherSnow:
		return weatherIconSnow
	case WeatherStorm:
		return weatherIconStorm
	default: // WeatherClouds + any unknown bucket
		return weatherIconClouds
	}
}

// WeatherColor returns the icon colour for a condition. Exported so cmd/ember can
// colour notification text to match the tile.
func WeatherColor(cond string) RGB {
	switch cond {
	case WeatherClear:
		return weatherColClear
	case WeatherFog:
		return weatherColFog
	case WeatherRain:
		return weatherColRain
	case WeatherSnow:
		return weatherColSnow
	case WeatherStorm:
		return weatherColStorm
	default:
		return weatherColClouds
	}
}

// WeatherPayload returns the rotating-tile frame: the 8×8 condition icon at cols
// 0–7 + the temperature text (e.g. "21°") drawn from col 9. Mirrors the usage
// tiles (no prio/force) so it rotates natively alongside them. lifetime seconds.
func WeatherPayload(cond, tempText string, lifetime int) map[string]any {
	var f Frame
	paintBitmap(&f, 0, 0, weatherIcon(cond), WeatherColor(cond))
	drawDigits(&f, tempText, 9, 1, colorWhite)
	return map[string]any{
		"draw":     []any{map[string]any{"db": []any{0, 0, 32, 8, framePixels(&f)}}},
		"lifetime": lifetime, "duration": 6,
	}
}

// WeatherPopupPayload returns a notification payload (drawn 8×8 icon + native
// scrolling label) for an ad-hoc weather popup. iconID, when non-empty, replaces
// the drawn icon with a native AWTRIX animated icon. durationSec controls how
// long the popup holds; sound (RTTTL or a device sound name), when non-empty,
// plays a chime (used for severe-weather alerts). The caller owns wakeup/stack.
func WeatherPopupPayload(cond, label, iconID string, durationSec int, sound string) map[string]any {
	p := map[string]any{
		"text":     label,
		"duration": durationSec,
		"wakeup":   true,
		"stack":    false,
		"color":    hexOf(WeatherColor(cond)),
	}
	if iconID != "" {
		// Native animated icon: AWTRIX reserves the left 8px and lays text out
		// in the remainder, so we must NOT set center/textOffset (see Pomodoro).
		p["icon"] = iconID
	} else {
		// Drawn icon as a db op at cols 0–7 + left-aligned native text from col 9.
		iconPx := bitmap8(weatherIcon(cond), WeatherColor(cond))
		p["draw"] = []any{map[string]any{"db": []any{0, 0, 8, 8, iconPx}}}
		p["center"] = false
		p["textOffset"] = 9
	}
	if sound != "" {
		p["sound"] = sound
	}
	return p
}

// hexOf formats an RGB as "RRGGBB" (no leading #) for AWTRIX colour fields.
func hexOf(c RGB) string { return fmt.Sprintf("%02X%02X%02X", c.R, c.G, c.B) }
