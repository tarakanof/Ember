package render

import (
	"testing"
	"time"
)

func TestIconBodySourceColoredEyesStateColored(t *testing.T) {
	col := "#3366FF"
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", SourceColor: &col}
	f := ComposeFrame(s, cardSource, []Session{s}, time.Now())

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
	f := ComposeFrame(s, cardSource, []Session{s}, time.Now())
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
	f := ComposeFrame(s, cardSource, []Session{s}, time.Now())
	if f.Pixels[0][0] != (RGB{0x33, 0x66, 0xFF}) {
		t.Fatalf("chevron = %v, want source colour", f.Pixels[0][0])
	}
	if f.Pixels[6][3] != colorError {
		t.Fatalf("cursor = %v, want %v", f.Pixels[6][3], colorError)
	}
}
