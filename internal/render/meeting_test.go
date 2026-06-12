package render

import "testing"

func TestMeetingPayloadShape(t *testing.T) {
	p := MeetingPayload("STANDUP", 12, 600)

	if p["text"] != "STANDUP 12m" {
		t.Errorf("text = %q, want %q", p["text"], "STANDUP 12m")
	}
	if p["color"] != hexOf(meetingInk) {
		t.Errorf("color = %v, want %v", p["color"], hexOf(meetingInk))
	}
	if p["lifetime"] != 600 {
		t.Errorf("lifetime = %v, want 600", p["lifetime"])
	}
	if p["duration"] != rotateDwellSeconds {
		t.Errorf("duration = %v, want %v", p["duration"], rotateDwellSeconds)
	}
	if p["center"] != false {
		t.Errorf("center = %v, want false", p["center"])
	}
	if p["textOffset"] != 9 {
		t.Errorf("textOffset = %v, want 9", p["textOffset"])
	}

	draw, ok := p["draw"].([]any)
	if !ok || len(draw) != 1 {
		t.Fatalf("draw must be a 1-element slice, got %v", p["draw"])
	}
	db, ok := draw[0].(map[string]any)["db"].([]any)
	if !ok || len(db) != 5 {
		t.Fatalf("draw[0].db must be a 5-element slice, got %v", draw[0])
	}
	pixels, ok := db[4].([]int)
	if !ok {
		t.Fatalf("draw[0].db[4] must be []int, got %T", db[4])
	}
	if len(pixels) != 64 {
		t.Errorf("icon pixel count = %d, want 64", len(pixels))
	}
}

func TestMeetingPopupPayloadShape(t *testing.T) {
	p := MeetingPopupPayload("STANDUP", 2, 30)

	if p["text"] != "STANDUP IN 2M" {
		t.Errorf("text = %q, want %q", p["text"], "STANDUP IN 2M")
	}
	if p["duration"] != 30 {
		t.Errorf("duration = %v, want 30", p["duration"])
	}
	if p["wakeup"] != true {
		t.Errorf("wakeup = %v, want true", p["wakeup"])
	}
	if p["stack"] != false {
		t.Errorf("stack = %v, want false", p["stack"])
	}
	if p["center"] != false {
		t.Errorf("center = %v, want false", p["center"])
	}
	if p["textOffset"] != 9 {
		t.Errorf("textOffset = %v, want 9", p["textOffset"])
	}
	if _, has := p["draw"]; !has {
		t.Error("popup must carry the drawn icon")
	}
	if _, has := p["sound"]; has {
		t.Error("popup must not carry a sound field")
	}
	if _, has := p["rtttl"]; has {
		t.Error("popup must not carry an rtttl field")
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
