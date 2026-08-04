package render

import "testing"

func TestAQIBuckets(t *testing.T) {
	cases := []struct {
		aqi  float64
		word string
		col  RGB
	}{
		{0, "GOOD", RGB{0x50, 0xF0, 0xE6}},
		{19.9, "GOOD", RGB{0x50, 0xF0, 0xE6}},
		{20, "FAIR", RGB{0x50, 0xCC, 0xAA}},
		{39.9, "FAIR", RGB{0x50, 0xCC, 0xAA}},
		{40, "MODERATE", RGB{0xF0, 0xE6, 0x41}},
		{60, "POOR", RGB{0xFF, 0x50, 0x50}},
		{80, "VERY POOR", RGB{0x96, 0x00, 0x32}},
		{100, "EXTREME", RGB{0x7D, 0x21, 0x81}},
		{173, "EXTREME", RGB{0x7D, 0x21, 0x81}},
	}
	for _, c := range cases {
		if got := AQIColor(c.aqi); got != c.col {
			t.Errorf("AQIColor(%v) = %v, want %v", c.aqi, got, c.col)
		}
		if got := AQIWord(c.aqi); got != c.word {
			t.Errorf("AQIWord(%v) = %q, want %q", c.aqi, got, c.word)
		}
	}
}

func TestAirTileFrame(t *testing.T) {
	// AQI 85 → "very poor" bucket; hourly strip spans three buckets.
	f := AirTileFrame(85, []float64{10, 50, 110})
	veryPoor := RGB{0x96, 0x00, 0x32}

	iconLit := false
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if f.Dirty[y][x] {
				iconLit = true
				if f.Pixels[y][x] != veryPoor {
					t.Fatalf("icon pixel (%d,%d) = %v, want bucket colour %v", x, y, f.Pixels[y][x], veryPoor)
				}
			}
		}
	}
	if !iconLit {
		t.Error("no icon pixels lit in cols 0–7")
	}

	digitLit := false
	for y := 0; y < 5; y++ {
		for x := 9; x < 32; x++ {
			if f.Dirty[y][x] {
				digitLit = true
				if f.Pixels[y][x] != veryPoor {
					t.Fatalf("digit pixel (%d,%d) = %v, want bucket colour %v", x, y, f.Pixels[y][x], veryPoor)
				}
			}
		}
	}
	if !digitLit {
		t.Error("no AQI digit pixels lit in cols 9–31 rows 0–4")
	}

	// Hourly strip: one column per hour from col 9, rows 6 and 7, each pixel in
	// that hour's own bucket colour.
	wantStrip := []RGB{{0x50, 0xF0, 0xE6}, {0xF0, 0xE6, 0x41}, {0x7D, 0x21, 0x81}}
	for i, want := range wantStrip {
		for _, y := range []int{6, 7} {
			if !f.Dirty[y][9+i] {
				t.Fatalf("strip pixel (%d,%d) not lit", 9+i, y)
			}
			if got := f.Pixels[y][9+i]; got != want {
				t.Errorf("strip pixel (%d,%d) = %v, want %v", 9+i, y, got, want)
			}
		}
	}
	// Col 8 stays a gutter between icon and body.
	for y := 0; y < 8; y++ {
		if f.Dirty[y][8] {
			t.Errorf("gutter col 8 lit at row %d", y)
		}
	}
}

func TestAirPayload(t *testing.T) {
	p := AirPayload(42, []float64{10, 20, 30}, 600)
	if p["lifetimeMs"] != 600_000 {
		t.Fatalf("lifetimeMs = %v, want 600000", p["lifetimeMs"])
	}
	if draw, ok := p["draw"].([]any); !ok || len(draw) != 1 {
		t.Fatalf("draw op missing: %v", p["draw"])
	}
	pixels := bmpPixels(t, p)
	if len(pixels) != 256 {
		t.Fatalf("frame pixel count = %d, want 256", len(pixels))
	}
}

func TestAirPopupPayload(t *testing.T) {
	p := AirPopupPayload(85, 30)
	if p["text"] != "AIR VERY POOR 85" {
		t.Errorf("text = %q, want AIR VERY POOR 85", p["text"])
	}
	if p["textColor"] != "#960032" {
		t.Errorf("textColor = %v, want #960032", p["textColor"])
	}
	if p["durationMs"] != 30_000 {
		t.Errorf("durationMs = %v, want 30000", p["durationMs"])
	}
	if _, has := p["draw"]; !has {
		t.Error("popup must carry the drawn icon")
	}
	if p["textCenter"] != false || p["textOffsetX"] != 9 {
		t.Errorf("popup must set textCenter:false textOffsetX:9, got center=%v offset=%v", p["textCenter"], p["textOffsetX"])
	}
	if _, has := p["sound"]; has {
		t.Error("air popup must not carry a sound field")
	}
}
