package render

import (
	"testing"
	"time"
)

// TestIconOverlaysMatchBodySprites verifies that the overlay bitmaps are
// consistent with the body sprites: Claude eyes must fall on holes in the
// body, and the Codex cursor must fall on lit pixels in the body.
// This test is a drift guard — it should always pass.
func TestIconOverlaysMatchBodySprites(t *testing.T) {
	lit := func(sprite []string, x, y int) bool { return sprite[y][x] == 'X' }
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if lit(claudeEyes8, x, y) && lit(usageIconClaude, x, y) {
				t.Errorf("claudeEyes8 (%d,%d) must be a HOLE in usageIconClaude", x, y)
			}
			if lit(codexCursor8, x, y) && !lit(usageIconCodex, x, y) {
				t.Errorf("codexCursor8 (%d,%d) must be LIT in usageIconCodex", x, y)
			}
		}
	}
	for _, s := range [][]string{claudeEyes8, codexCursor8} {
		if len(s) != 8 {
			t.Fatalf("overlay must be 8 rows, got %d", len(s))
		}
		for i, row := range s {
			if len(row) != 8 {
				t.Fatalf("overlay row %d must be 8 cols, got %d", i, len(row))
			}
		}
	}
}

func TestRenderIdleFrameKeepsEyeSocketsDark(t *testing.T) {
	p := RenderIdleFrame(30)
	pixels := bmpPixels(t, p)
	// Eye socket (row 2, col 2) of usageIconClaude is a hole — must stay 0.
	if pixels[2*8+2] != 0 {
		t.Fatalf("idle eye socket lit: %#x", pixels[2*8+2])
	}
	// Body pixel (row 1, col 1) must be the dim gray.
	if pixels[1*8+1] != 0x666666 {
		t.Fatalf("idle body = %#x, want 0x666666", pixels[1*8+1])
	}
}

func TestIconInvalidHexFallsBackToNeutral(t *testing.T) {
	bad := "not-a-colour"
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", SourceColor: &bad}
	f := ComposeFrame(s, cardSource, nil, []Session{s}, time.Now())
	// Body pixel at (row 1, col 1) must use iconNeutral when source colour is invalid.
	if f.Pixels[1][1] != iconNeutral {
		t.Fatalf("body with invalid hex = %v, want iconNeutral %v", f.Pixels[1][1], iconNeutral)
	}
}

func TestIconBodySourceColoredEyesStateColored(t *testing.T) {
	col := "#3366FF"
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", SourceColor: &col}
	f := ComposeFrame(s, cardSource, nil, []Session{s}, time.Now())

	body := RGB{0x33, 0x66, 0xFF}
	// Head-top pixel (row 1, col 1 of usageIconClaude) carries the source colour.
	if f.Pixels[1][1] != body {
		t.Fatalf("body pixel = %v, want source colour %v", f.Pixels[1][1], body)
	}
	// Eye pixel (row 2, col 2 — a hole in the body sprite) carries the state colour.
	if !f.Dirty[2][2] || f.Pixels[2][2] != colorRunning {
		t.Fatalf("eye pixel = %v dirty=%v, want %v", f.Pixels[2][2], f.Dirty[2][2], colorRunning)
	}
}

func TestIconNeutralFallbackWithoutSourceColor(t *testing.T) {
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "waiting"}
	f := ComposeFrame(s, cardSource, nil, []Session{s}, time.Now())
	if f.Pixels[1][1] != iconNeutral {
		t.Fatalf("body = %v, want neutral %v", f.Pixels[1][1], iconNeutral)
	}
	if f.Pixels[2][2] != colorWaiting {
		t.Fatalf("eye = %v, want %v", f.Pixels[2][2], colorWaiting)
	}
}

func TestCodexCursorStateColored(t *testing.T) {
	col := "#3366FF"
	s := Session{Source: "mbp", Tool: "codex", Session: "s1", State: "error", SourceColor: &col}
	f := ComposeFrame(s, cardSource, nil, []Session{s}, time.Now())
	if f.Pixels[0][0] != (RGB{0x33, 0x66, 0xFF}) {
		t.Fatalf("chevron = %v, want source colour", f.Pixels[0][0])
	}
	if f.Pixels[6][3] != colorError {
		t.Fatalf("cursor = %v, want %v", f.Pixels[6][3], colorError)
	}
}
