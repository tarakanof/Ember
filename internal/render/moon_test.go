package render

import (
	"strings"
	"testing"
)

func litCount(rows []string) int {
	n := 0
	for _, r := range rows {
		n += strings.Count(r, "X")
	}
	return n
}

func TestMoonSpriteFullNewHalf(t *testing.T) {
	discTotal := litCount(moonSprite(MoonView{Illum: 1, Waxing: true}))
	if discTotal == 0 {
		t.Fatal("full moon drew nothing")
	}
	// New moon is dark.
	if n := litCount(moonSprite(MoonView{Illum: 0, Waxing: true})); n != 0 {
		t.Errorf("new moon lit %d pixels, want 0", n)
	}
	// Half moon lights roughly half the disc.
	half := litCount(moonSprite(MoonView{Illum: 0.5, Waxing: true}))
	if half == 0 || half >= discTotal {
		t.Errorf("half moon lit %d, want between 0 and %d", half, discTotal)
	}
}

func TestMoonSpriteWaxingVsWaning(t *testing.T) {
	wax := moonSprite(MoonView{Illum: 0.5, Waxing: true})
	wan := moonSprite(MoonView{Illum: 0.5, Waxing: false})
	// Middle row spans the full width (cols 0–7). Waxing lights the right limb,
	// waning the left.
	if wax[3][7] != 'X' || wax[3][0] != '.' {
		t.Errorf("waxing half should light the right limb: %q", wax[3])
	}
	if wan[3][0] != 'X' || wan[3][7] != '.' {
		t.Errorf("waning half should light the left limb: %q", wan[3])
	}
}

func TestWeatherPayloadMoonUsesMoonIcon(t *testing.T) {
	// The clear-night tile should differ in the icon region (cols 0–7) from the
	// day (sun) tile, proving the moon was drawn there.
	day := WeatherPayload(WeatherClear, "12°", nil, 600)
	night := WeatherPayloadMoon("12°", nil, MoonView{Illum: 0.5, Waxing: true}, 600)
	dp := day["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	np := night["draw"].([]any)[0].(map[string]any)["db"].([]any)[4].([]int)
	same := true
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if dp[y*32+x] != np[y*32+x] {
				same = false
			}
		}
	}
	if same {
		t.Error("moon tile icon region matches the sun tile — moon not drawn")
	}
}

func TestSunPopupPayload(t *testing.T) {
	rise := SunPopupPayload(true, "SUNRISE 5:21", 30)
	if rise["text"] != "SUNRISE 5:21" || rise["center"] != false || rise["textOffset"] != 9 {
		t.Errorf("sunrise popup layout wrong: %+v", rise)
	}
	if _, has := rise["draw"]; !has {
		t.Error("sun popup must carry a draw op")
	}
	if rise["color"] != hexOf(sunriseColor) {
		t.Errorf("sunrise colour = %v, want %v", rise["color"], hexOf(sunriseColor))
	}
	set := SunPopupPayload(false, "SUNSET 21:08", 30)
	if set["color"] != hexOf(sunsetColor) {
		t.Errorf("sunset colour = %v, want %v", set["color"], hexOf(sunsetColor))
	}
	// No sound on the notification (firmware drops it under a draw op).
	if _, has := rise["sound"]; has {
		t.Error("sun popup must not carry a sound field")
	}
}
