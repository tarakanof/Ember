package render

import "testing"

func TestMeetingPayloadShape(t *testing.T) {
	p := MeetingPayload("STANDUP", 12, 600)

	// The countdown leads: a long title scrolls, and the minutes are what
	// the tile is for.
	if p["text"] != "12M STANDUP" {
		t.Errorf("text = %q, want %q", p["text"], "12M STANDUP")
	}
	assertPinnedText(t, p)
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
	if pixels := bmpPixels(t, p); len(pixels) != iconOpW*8 {
		t.Errorf("icon pixel count = %d, want %d", len(pixels), iconOpW*8)
	}
}

func TestMeetingPopupPayloadShape(t *testing.T) {
	p := MeetingPopupPayload("STANDUP", 2, 30)

	if p["text"] != "STANDUP IN 2M" {
		t.Errorf("text = %q, want %q", p["text"], "STANDUP IN 2M")
	}
	assertPinnedText(t, p)
	if p["repeat"] != 1 {
		t.Errorf("repeat = %v, want 1 (a long title is read to the end)", p["repeat"])
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

// assertPinnedText checks that a text payload pins the two text behaviours NG
// otherwise inherits from the device's global settings: uppercase (the
// previews always draw uppercase) and "still when it fits, scroll when not".
func assertPinnedText(t *testing.T, p map[string]any) {
	t.Helper()
	if p["textCase"] != "upper" {
		t.Errorf("textCase = %v, want upper", p["textCase"])
	}
	scroll, _ := p["scroll"].(map[string]any)
	if scroll["whenFits"] != "static" {
		t.Errorf("scroll = %v, want whenFits:static", p["scroll"])
	}
}

// TestMeetingTileFrameLeadsWithMinutes: the preview shows what the device
// shows at rest, the countdown first ("12M" from col 9).
func TestMeetingTileFrameLeadsWithMinutes(t *testing.T) {
	f := MeetingTileFrame("STANDUP", 12)
	var want Frame
	drawDigits(&want, "12M", contentX, textRow, meetingInk)
	for y := textRow; y < textRow+5; y++ {
		for x := contentX; x < contentX+11; x++ {
			if f.Dirty[y][x] != want.Dirty[y][x] {
				t.Fatalf("pixel (%d,%d) lit=%v, want %v: tile must start with \"12M\"", x, y, f.Dirty[y][x], want.Dirty[y][x])
			}
		}
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
