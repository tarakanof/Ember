package render

import (
	"strings"
	"testing"
	"time"
)

func waitingSnap() Snapshot {
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "waiting",
		Activity: "Bash: npm test", UpdatedAt: time.Now()}
	return Snapshot{Now: time.Now(), Sessions: []Session{s}}
}

func TestAttentionFrameNamesSource(t *testing.T) {
	snap := waitingSnap()
	p := RenderForCoord(snap, snap.Sessions[0].Key(), 0, true, 30, nil)
	text, _ := p["text"].(string)
	if text != "WAIT MBP" {
		t.Fatalf("attention text = %q, want %q", text, "WAIT MBP")
	}
	if _, hasBlink := p["blinkText"]; !hasBlink {
		t.Fatal("attention frame must blink")
	}
	// 8 chars ≈ 32 px > the 22 free cols — must be allowed to scroll.
	if _, hasNoScroll := p["noScroll"]; hasNoScroll {
		t.Fatal("long attention text must not set noScroll")
	}
	if strings.Contains(text, "Bash") {
		t.Fatal("activity detail must not replace the attention label")
	}
}

func TestAttentionFrameShortLabelNoScroll(t *testing.T) {
	// Empty source: bare WAIT fits the free columns — keep noScroll for a
	// steady blink.
	s := Session{Source: "", Tool: "claude", Session: "s1", State: "waiting", UpdatedAt: time.Now()}
	snap := Snapshot{Now: time.Now(), Sessions: []Session{s}}
	p := RenderForCoord(snap, s.Key(), 0, true, 30, nil)
	if text, _ := p["text"].(string); text != "WAIT" {
		t.Fatalf("text = %q, want WAIT", text)
	}
	if _, hasNoScroll := p["noScroll"]; !hasNoScroll {
		t.Fatal("short attention text should keep noScroll")
	}
}
