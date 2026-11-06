package render

import "strings"

const (
	WeatherClear  = "clear"
	WeatherClouds = "clouds"
	WeatherFog    = "fog"
	WeatherRain   = "rain"
	WeatherSnow   = "snow"
	WeatherStorm  = "storm"
)

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
	weatherColClear  = RGB{0xff, 0xc1, 0x4d}
	weatherColClouds = RGB{0x9a, 0xa3, 0xad}
	weatherColFog    = RGB{0x80, 0x88, 0x90}
	weatherColRain   = RGB{0x4f, 0xa9, 0xff}
	weatherColSnow   = RGB{0xe6, 0xf0, 0xff}
	weatherColStorm  = RGB{0xb1, 0x6c, 0xff}
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
	default:
		return weatherIconClouds
	}
}

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

func WeatherPayload(cond, tempText string, tempC float64, hourly []float64, lifetime int) map[string]any {
	return weatherTile(cond, tempText, tempC, hourly, nil, lifetime)
}

func WeatherPayloadMoon(tempText string, tempC float64, hourly []float64, moon MoonView, lifetime int) map[string]any {
	return weatherTile("", tempText, tempC, hourly, &moon, lifetime)
}

func drawWeatherBody(f *Frame, tempText string, tempC float64, hourly []float64) {
	x := centredX(tempText)
	digits := strings.TrimSuffix(tempText, "°")
	drawDigits(f, digits, x, textRow, TempColor(tempC))
	if digits != tempText {
		drawDigits(f, "°", x+4*len([]rune(digits)), textRow, colorWhite)
	}
	drawForecastStrip(f, hourly)
}

func WeatherTileFrame(cond, tempText string, tempC float64, hourly []float64, moon *MoonView) Frame {
	var f Frame
	if moon != nil {
		paintBitmap(&f, 0, 0, moonSprite(*moon), moonColor)
	} else {
		paintBitmap(&f, 0, 0, weatherIcon(cond), WeatherColor(cond))
	}
	drawWeatherBody(&f, tempText, tempC, hourly)
	return f
}

func weatherTile(cond, tempText string, tempC float64, hourly []float64, moon *MoonView, lifetime int) map[string]any {
	f := WeatherTileFrame(cond, tempText, tempC, hourly, moon)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}

func WeatherPayloadNative(iconID, tempText string, tempC float64, hourly []float64, lifetime int) map[string]any {
	var f Frame
	drawWeatherBody(&f, tempText, tempC, hourly)
	return map[string]any{
		"icon":       iconID,
		"draw":       []any{bitmapOp(iconW, 0, panelW-iconW, 8, framePixelsRect(&f, iconW, 0, panelW-iconW, 8))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}

func WeatherPopupPayload(cond, label, iconID string, durationSec int) map[string]any {
	p := readOnce(map[string]any{
		"text":       label,
		"durationMs": msOf(durationSec),
		"wakeup":     true,
		"textColor":  hexOf(WeatherColor(cond)),
	})
	if iconID != "" {
		p["icon"] = iconID
	} else {
		iconPx := bitmap8(weatherIcon(cond), WeatherColor(cond))
		p["draw"] = []any{iconOp(iconPx)}
		p["textCenter"] = false
		p["textOffsetX"] = 9
	}
	return p
}
