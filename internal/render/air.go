package render

import (
	"fmt"
	"math"
)

type aqiStop struct {
	lt   float64
	word string
	c    RGB
}

var aqiBuckets = []aqiStop{
	{20, "GOOD", RGB{0x50, 0xF0, 0xE6}},
	{40, "FAIR", RGB{0x50, 0xCC, 0xAA}},
	{60, "MODERATE", RGB{0xF0, 0xE6, 0x41}},
	{80, "POOR", RGB{0xFF, 0x50, 0x50}},
	{100, "VERY POOR", RGB{0xFF, 0x10, 0x60}},
	{math.Inf(1), "EXTREME", RGB{0xC0, 0x40, 0xFF}},
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

func drawAQIStrip(f *Frame, hourly []float64) {
	drawHourlyStrip(f, len(hourly), func(i int) RGB { return AQIColor(hourly[i]) })
}

// AirTileFrame composes the drawn air-quality tile: wind icon at cols 0–7 and
// the rounded AQI value centred in the content area (rows 1–5), both in the
// current bucket colour, plus the hourly-AQI strip on the bottom bar (row 7,
// cols 8–31).
func AirTileFrame(aqi float64, hourly []float64) Frame {
	var f Frame
	col := AQIColor(aqi)
	paintBitmap(&f, 0, 0, airIcon, col)
	text := fmt.Sprintf("%d", int(math.Round(aqi)))
	drawDigits(&f, text, centredX(text), textRow, col)
	drawAQIStrip(&f, hourly)
	return f
}

// AirPayload renders the rotating air-quality tile.
func AirPayload(aqi float64, hourly []float64, lifetime int) map[string]any {
	f := AirTileFrame(aqi, hourly)
	return map[string]any{
		"draw":       []any{bitmapOp(0, 0, 32, 8, framePixels(&f))},
		"lifetimeMs": msOf(lifetime), "durationMs": msOf(rotateDwellSeconds),
	}
}

// AirPopupPayload returns the threshold-crossing popup: drawn icon at cols
// 0–7 + native scrolling "AIR <WORD> <N>" text, both in the bucket colour.
func AirPopupPayload(aqi float64, durationSec int) map[string]any {
	col := AQIColor(aqi)
	iconPx := bitmap8(airIcon, col)
	return map[string]any{
		"text":        fmt.Sprintf("AIR %s %d", AQIWord(aqi), int(math.Round(aqi))),
		"textColor":   hexOf(col),
		"durationMs":  msOf(durationSec),
		"wakeup":      true,
		"stack":       false,
		"draw":        []any{iconOp(iconPx)},
		"textCenter":  false,
		"textOffsetX": 9,
	}
}
