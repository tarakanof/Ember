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

func TestLimitResetPopupPayload(t *testing.T) {
	p := LimitResetPopupPayload("claude", 10)
	if p["text"] != "CLAUDE 5H RESET" {
		t.Fatalf("text = %v", p["text"])
	}
	if _, hasHold := p["hold"]; hasHold {
		t.Fatal("popup must auto-dismiss (no hold)")
	}
	if p["durationMs"] != 10_000 {
		t.Fatalf("durationMs = %v, want 10000", p["durationMs"])
	}
	if _, ok := p["draw"]; !ok {
		t.Fatal("expected drawn tool icon")
	}
	if p2 := LimitResetPopupPayload("codex", 10); p2["text"] != "CODEX 5H RESET" {
		t.Fatalf("codex text = %v", p2["text"])
	}
}
