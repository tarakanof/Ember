package render

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
// 0–7 + the temperature text (e.g. "21°") drawn from col 9, plus a compact
// hourly-forecast strip on the bottom row (one colour-coded pixel per hour, cols
// 9–31 ≈ next 23h) when `hourly` is supplied. Mirrors the usage tiles (no
// prio/force) so it rotates natively alongside them. lifetime seconds.
func WeatherPayload(cond, tempText string, hourly []float64, lifetime int) map[string]any {
	return weatherTile(cond, tempText, hourly, nil, lifetime)
}

// WeatherPayloadMoon is the clear-night variant: the left 8×8 icon shows the
// moon phase (see MoonView) instead of the condition icon. Used when the caller
// has determined it's night and the moon-phase option is on.
func WeatherPayloadMoon(tempText string, hourly []float64, moon MoonView, lifetime int) map[string]any {
	return weatherTile("", tempText, hourly, &moon, lifetime)
}

// drawWeatherBody paints the conditions tile's right side (cols 9-31): the
// temperature digits centred in the free area (rows 0-4) and the 2px-tall
// hourly strip (rows 6-7). Shared by the drawn tile, the native-icon variant,
// and the moon variant so the layout can't drift.
func drawWeatherBody(f *Frame, tempText string, hourly []float64) {
	textW := len([]rune(tempText))*4 - 1
	x := 9 + (23-textW)/2
	if x < 9 {
		x = 9
	}
	drawDigits(f, tempText, x, 0, colorWhite)
	drawForecastStrip(f, hourly, 9, 31, 6)
	drawForecastStrip(f, hourly, 9, 31, 7)
}

// WeatherTileFrame composes the drawn rotating-tile frame: condition icon (or
// moon phase when moon is non-nil), centred temperature digits, 2px-tall
// hourly strip. Shared by the device payload and /v1/weather/preview.
func WeatherTileFrame(cond, tempText string, hourly []float64, moon *MoonView) Frame {
	var f Frame
	if moon != nil {
		paintBitmap(&f, 0, 0, moonSprite(*moon), moonColor)
	} else {
		paintBitmap(&f, 0, 0, weatherIcon(cond), WeatherColor(cond))
	}
	drawWeatherBody(&f, tempText, hourly)
	return f
}

func weatherTile(cond, tempText string, hourly []float64, moon *MoonView, lifetime int) map[string]any {
	f := WeatherTileFrame(cond, tempText, hourly, moon)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}

// WeatherPayloadNative is the native-icon variant of WeatherPayload: the
// animated AWTRIX/LaMetric icon (resolved by the caller via the popup icon-ID
// mapping) occupies cols 0-7 while the temperature digits and the hourly strip
// stay drawn, emitted as a partial bitmap over cols 8-31 — identical layout to
// the drawn tile, no firmware text layout involved. Re-verified on awtrix-ng
// 1.0.13: bitmap coords are absolute and a native icon coexists with a partial
// bitmap covering the rest of the panel, both visible in the same frame. The icon
// paints last of the two, so the bitmap must stay clear of cols 0-7 (it does).
// Gallery icons download on first reference, so a fresh ID can be blank for a
// few seconds — ensureWeatherIcons provisions them up front for that reason.
func WeatherPayloadNative(iconID, tempText string, hourly []float64, lifetime int) map[string]any {
	var f Frame
	drawWeatherBody(&f, tempText, hourly)
	return map[string]any{
		"icon":       iconID,
		"draw":       []any{bitmapOp(8, 0, 24, 8, framePixelsRect(&f, 8, 0, 24, 8))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}

// WeatherPopupPayload returns a notification payload (drawn 8×8 icon + native
// scrolling label) for an ad-hoc weather popup. iconID, when non-empty, replaces
// the drawn icon with a native AWTRIX animated icon. durationSec controls how
// long the popup holds. The caller owns wakeup/stack.
//
// The severe-alert chime is NOT carried here: on AWTRIX 0.98 a notification's
// `sound` is silently dropped whenever the notification also has a `draw` or
// `icon` (which a weather popup always does), so the caller plays it separately
// via /api/rtttl (RTTTL) or /api/sound (device melody name).
func WeatherPopupPayload(cond, label, iconID string, durationSec int) map[string]any {
	p := map[string]any{
		"text":       label,
		"durationMs": msOf(durationSec),
		"wakeup":     true,
		"stack":      false,
		"textColor":  hexOf(WeatherColor(cond)),
	}
	if iconID != "" {
		// Native animated icon: the firmware reserves the left 8px and lays text
		// out in the remainder, so we must NOT set textCenter/textOffsetX (see
		// Pomodoro).
		p["icon"] = iconID
	} else {
		// Drawn icon as a bitmap op at cols 0–7 + left-aligned text from col 9.
		iconPx := bitmap8(weatherIcon(cond), WeatherColor(cond))
		p["draw"] = []any{bitmapOp(0, 0, 8, 8, iconPx)}
		p["textCenter"] = false
		p["textOffsetX"] = 9
	}
	return p
}
