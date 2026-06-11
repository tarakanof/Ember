package render

import (
	"testing"
)

func TestUsageBarFill(t *testing.T) {
	// 24-wide content bar, 50% -> 12 filled
	px := usageBarPixels(50)
	if len(px) != 24 {
		t.Fatalf("len = %d", len(px))
	}
	filled := 0
	for _, c := range px {
		if c == dimThreshold(50) {
			filled++
		}
	}
	if filled != 12 {
		t.Errorf("filled = %d, want 12", filled)
	}
}

func TestUsageIconsAre8x8(t *testing.T) {
	for _, s := range [][]string{usageIconClaude, usageIconCodex} {
		if len(s) != 8 {
			t.Fatalf("icon height = %d", len(s))
		}
		for _, row := range s {
			if len(row) != 8 {
				t.Fatalf("icon width = %d", len(row))
			}
		}
	}
}

func TestWeeklyPayloadIsNativeTextLeftAligned(t *testing.T) {
	p := UsageWeeklyPayload(usageIconClaude, usageColorClaude, "MON", 72, '7', 'd', 30)
	if p["text"] != "MON" {
		t.Errorf("text = %v", p["text"])
	}
	if p["center"] != false {
		t.Error("center must be false (literal textOffset)")
	}
	if p["textOffset"] != 9 {
		t.Errorf("textOffset = %v", p["textOffset"])
	}
	if p["noScroll"] != true {
		t.Error("noScroll must be true")
	}
	draws := p["draw"].([]any)
	if len(draws) != 3 {
		t.Errorf("want 3 draw ops (icon+unit+bar), got %d", len(draws))
	}
}

func TestFiveHourPayloadIsSingleDrawNoText(t *testing.T) {
	p := UsageFiveHourPayload(usageIconClaude, usageColorClaude, "14:25", 15, 30)
	if _, hasText := p["text"]; hasText {
		t.Error("5h frame is fully drawn, no native text")
	}
	draws := p["draw"].([]any)
	if len(draws) != 1 {
		t.Errorf("want 1 full-frame draw, got %d", len(draws))
	}
}

func TestLimitResetPopupPayload(t *testing.T) {
	p := LimitResetPopupPayload("claude", 10)
	if p["text"] != "CLAUDE 5H RESET" {
		t.Fatalf("text = %v", p["text"])
	}
	if _, hasHold := p["hold"]; hasHold {
		t.Fatal("popup must auto-dismiss (no hold)")
	}
	if p["duration"] != 10 {
		t.Fatalf("duration = %v", p["duration"])
	}
	if _, ok := p["draw"]; !ok {
		t.Fatal("expected drawn tool icon")
	}
	if p2 := LimitResetPopupPayload("codex", 10); p2["text"] != "CODEX 5H RESET" {
		t.Fatalf("codex text = %v", p2["text"])
	}
}

func TestModelMarkerGlyphsAreGray(t *testing.T) {
	// Per-model frames render the OP/SO marker with no leading digit; both
	// glyphs must be gray (never the tool colour).
	p := UsageModelPayload(usageIconClaude, usageColorClaude, "MON", "opus", 82, 30)
	draws := p["draw"].([]any)
	// unit is the 2nd draw op (db [26,1,6,5]).
	unitDB := draws[1].(map[string]any)["db"].([]any)
	unitPx := unitDB[4].([]int)
	gray := toInt(usageGray)
	tool := toInt(usageColorClaude)
	lit := 0
	for _, v := range unitPx {
		if v == tool {
			t.Errorf("model marker pixel painted in tool colour, want gray")
		}
		if v == gray {
			lit++
		}
	}
	if lit == 0 {
		t.Error("model marker rendered no gray pixels")
	}
}
