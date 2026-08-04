package render

import "testing"

func TestMeetingPayloadShape(t *testing.T) {
	p := MeetingPayload("STANDUP", 12, 600)

	if p["text"] != "STANDUP 12m" {
		t.Errorf("text = %q, want %q", p["text"], "STANDUP 12m")
	}
	if p["textColor"] != hexOf(meetingInk) {
		t.Errorf("textColor = %v, want %v", p["textColor"], hexOf(meetingInk))
	}
	if p["lifetimeMs"] != 600_000 {
		t.Errorf("lifetimeMs = %v, want 600000", p["lifetimeMs"])
	}
	if p["durationMs"] != msOf(rotateDwellSeconds) {
		t.Errorf("durationMs = %v, want %v", p["durationMs"], msOf(rotateDwellSeconds))
	}
	if p["textCenter"] != false {
		t.Errorf("textCenter = %v, want false", p["textCenter"])
	}
	if p["textOffsetX"] != 9 {
		t.Errorf("textOffsetX = %v, want 9", p["textOffsetX"])
	}

	if draw, ok := p["draw"].([]any); !ok || len(draw) != 1 {
		t.Fatalf("draw must be a 1-element slice, got %v", p["draw"])
	}
	if pixels := bmpPixels(t, p); len(pixels) != 64 {
		t.Errorf("icon pixel count = %d, want 64", len(pixels))
	}
}

func TestMeetingPopupPayloadShape(t *testing.T) {
	p := MeetingPopupPayload("STANDUP", 2, 30)

	if p["text"] != "STANDUP IN 2M" {
		t.Errorf("text = %q, want %q", p["text"], "STANDUP IN 2M")
	}
	if p["durationMs"] != 30_000 {
		t.Errorf("durationMs = %v, want 30000", p["durationMs"])
	}
	if p["wakeup"] != true {
		t.Errorf("wakeup = %v, want true", p["wakeup"])
	}
	if p["stack"] != true {
		t.Errorf("stack = %v, want true", p["stack"])
	}
	if p["textCenter"] != false {
		t.Errorf("textCenter = %v, want false", p["textCenter"])
	}
	if p["textOffsetX"] != 9 {
		t.Errorf("textOffsetX = %v, want 9", p["textOffsetX"])
	}
	if _, has := p["draw"]; !has {
		t.Error("popup must carry the drawn icon")
	}
	if _, has := p["sound"]; has {
		t.Error("popup must not carry a sound field")
	}
	if _, has := p["soundRtttl"]; has {
		t.Error("popup must not carry a soundRtttl field")
	}
}

func TestMeetingTileFrame(t *testing.T) {
	// Lowercase title must render identically to uppercase (font3x5 has no
	// lowercase glyphs; the function must uppercase before drawing).
	fLower := MeetingTileFrame("standup", 12)
	fUpper := MeetingTileFrame("STANDUP", 12)
	if fLower != fUpper {
		t.Error("MeetingTileFrame(\"standup\", 12) pixels differ from MeetingTileFrame(\"STANDUP\", 12): title must be uppercased before drawing")
	}

	f := MeetingTileFrame("STANDUP", 12)

	iconLit := false
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if f.Dirty[y][x] {
				iconLit = true
				break
			}
		}
		if iconLit {
			break
		}
	}
	if !iconLit {
		t.Error("no lit pixels in cols 0–7 (icon region)")
	}

	textLit := false
	for y := 0; y < 8; y++ {
		for x := 9; x < 32; x++ {
			if f.Dirty[y][x] {
				textLit = true
				break
			}
		}
		if textLit {
			break
		}
	}
	if !textLit {
		t.Error("no lit pixels at col >= 9 (text region)")
	}
}
