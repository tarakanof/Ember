package render

import (
	"fmt"
	"math"
)

// Air-quality render primitives. The air app is a rotating tile in the weather
// family: an 8×8 drawn wind icon + the current European AQI (EAQI) value, both
// painted in the official EEA bucket colour (the colour IS the reading, unlike
// the weather tile's white digits), plus a 2px hourly-AQI strip on the bottom
// rows. The popup fires on a configured threshold crossing.

// aqiStop is one EAQI bucket: readings below `lt` take this word + colour.
type aqiStop struct {
	lt   float64
	word string
	c    RGB
}

// aqiBuckets is the official EEA European-AQI scale (eea.europa.eu). Discrete
// buckets, no interpolation — the scale itself is bucketed.
var aqiBuckets = []aqiStop{
	{20, "GOOD", RGB{0x50, 0xF0, 0xE6}},
	{40, "FAIR", RGB{0x50, 0xCC, 0xAA}},
	{60, "MODERATE", RGB{0xF0, 0xE6, 0x41}},
	{80, "POOR", RGB{0xFF, 0x50, 0x50}},
	{100, "VERY POOR", RGB{0x96, 0x00, 0x32}},
	{math.Inf(1), "EXTREME", RGB{0x7D, 0x21, 0x81}},
}

func aqiBucket(aqi float64) aqiStop {
	for _, b := range aqiBuckets {
		if aqi < b.lt {
			return b
		}
	}
	return aqiBuckets[len(aqiBuckets)-1]
}

// AQIColor returns the EEA bucket colour for a European AQI reading.
func AQIColor(aqi float64) RGB { return aqiBucket(aqi).c }

// AQIWord returns the EEA bucket word ("GOOD".."EXTREME") for a reading.
func AQIWord(aqi float64) string { return aqiBucket(aqi).word }

// airIcon is the 8×8 wind/air sprite: streaking gusts with curled tails.
var airIcon = []string{
	"........",
	".XXX....",
	"....X...",
	"XXXXXXX.",
	"........",
	".XXXXX..",
	"......X.",
	".....X..",
}

// drawAQIStrip paints one pixel per hourly AQI value across row y from x0..x1
// (inclusive), each in its own bucket colour. Mirrors drawForecastStrip.
func drawAQIStrip(f *Frame, hourly []float64, x0, x1, y int) {
	x := x0
	for _, v := range hourly {
		if x > x1 {
			break
		}
		paintCell(f, x, y, AQIColor(v))
		x++
	}
}

// AirTileFrame composes the drawn air-quality tile: wind icon at cols 0–7 and
// the rounded AQI value centred over cols 9–31 (rows 0–4), both in the current
// bucket colour, plus the 2px hourly-AQI strip (rows 6–7, one pixel per hour).
// Shared by the device payload and /v1/weather/preview.
func AirTileFrame(aqi float64, hourly []float64) Frame {
	var f Frame
	col := AQIColor(aqi)
	paintBitmap(&f, 0, 0, airIcon, col)
	text := fmt.Sprintf("%d", int(math.Round(aqi)))
	textW := len([]rune(text))*4 - 1
	x := 9 + (23-textW)/2
	if x < 9 {
		x = 9
	}
	drawDigits(&f, text, x, 0, col)
	drawAQIStrip(&f, hourly, 9, 31, 6)
	drawAQIStrip(&f, hourly, 9, 31, 7)
	return f
}

// AirPayload renders the rotating air-quality tile. Mirrors the weather tiles
// (full-frame db, no prio/force) so it rotates natively. lifetime seconds.
func AirPayload(aqi float64, hourly []float64, lifetime int) map[string]any {
	f := AirTileFrame(aqi, hourly)
	return map[string]any{
		"draw":     []any{map[string]any{"db": []any{0, 0, 32, 8, framePixels(&f)}}},
		"lifetime": lifetime, "duration": rotateDwellSeconds,
	}
}

// AirPopupPayload returns the threshold-crossing popup: drawn icon at cols 0–7
// + native scrolling "AIR <WORD> <N>" text, both in the bucket colour. No
// sound — poor air isn't a severe-weather event. durationSec holds the popup.
func AirPopupPayload(aqi float64, durationSec int) map[string]any {
	col := AQIColor(aqi)
	iconPx := bitmap8(airIcon, col)
	return map[string]any{
		"text":       fmt.Sprintf("AIR %s %d", AQIWord(aqi), int(math.Round(aqi))),
		"color":      hexOf(col),
		"duration":   durationSec,
		"wakeup":     true,
		"stack":      false,
		"draw":       []any{map[string]any{"db": []any{0, 0, 8, 8, iconPx}}},
		"center":     false,
		"textOffset": 9,
	}
}
