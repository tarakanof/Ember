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
	if _, hasBlink := p["textBlinkMs"]; !hasBlink {
		t.Fatal("attention frame must blink")
	}
	if strings.Contains(text, "Bash") {
		t.Fatal("activity detail must not replace the attention label")
	}
}

// TestAttentionFrameDisablesCenteringForTheOffset guards the geometry verified
// on firmware 1.0.13: textOffsetX is added to the centred position rather than
// replacing it, so an attention frame that left textCenter at its default true
// would centre the label over its own sprite and then shove it 9px right (first
// glyph at col 19 instead of 9, clipping longer labels off the panel).
func TestAttentionFrameDisablesCenteringForTheOffset(t *testing.T) {
	snap := waitingSnap()
	p := RenderForCoord(snap, snap.Sessions[0].Key(), 0, true, 30, nil)

	if got, ok := p["textCenter"].(bool); !ok || got {
		t.Fatalf("textCenter = %v, want an explicit false", p["textCenter"])
	}
	if got := p["textOffsetX"]; got != 9 {
		t.Fatalf("textOffsetX = %v, want 9 (the column after the 8px sprite)", got)
	}
	if _, has := p["icon"]; has {
		t.Fatal("the sprite is a draw bitmap, which indents nothing — a native icon would double-indent")
	}
}

// AttentionHeld must agree with what RenderForCoord actually emits: the
// coordinator pins the app device-side off this answer, and a disagreement means
// either an unheld frame stealing the screen or a held one never getting it.
func TestAttentionHeldMatchesTheEmittedFrame(t *testing.T) {
	held := func(p map[string]any) bool {
		return p != nil && p["durationMs"] == p["lifetimeMs"]
	}
	cases := []struct {
		name    string
		state   string
		pointer string
		locked  bool
	}{
		{"locked waiting", "waiting", "mbp/claude/s1", true},
		{"locked error", "error", "mbp/claude/s1", true},
		{"locked running", "running", "mbp/claude/s1", true},
		{"unlocked waiting", "waiting", "mbp/claude/s1", false},
		{"locked waiting, stale pointer falls back", "waiting", "gone/claude/x", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := waitingSnap()
			snap.Sessions[0].State = c.state
			p := RenderForCoord(snap, c.pointer, 0, c.locked, 30, nil)
			if got, want := AttentionHeld(snap, c.pointer, c.locked), held(p); got != want {
				t.Fatalf("AttentionHeld = %v, but the emitted frame holds = %v", got, want)
			}
		})
	}
}

// An empty snapshot publishes nothing, so nothing can claim the hold.
func TestAttentionHeldFalseWithoutSessions(t *testing.T) {
	if AttentionHeld(Snapshot{Now: time.Now()}, "", true) {
		t.Fatal("AttentionHeld with no sessions = true, want false")
	}
}

// TestAttentionFrameDefersFitToScrollWhenFits pins the native replacement for
// the old len(text)<=5 gate: the firmware decides whether the label moves, so
// both a short label ("WAIT") and a long one ("WAIT MBP") carry the identical
// scroll object and the payload never counts characters itself.
func TestAttentionFrameDefersFitToScrollWhenFits(t *testing.T) {
	long := waitingSnap()
	pLong := RenderForCoord(long, long.Sessions[0].Key(), 0, true, 30, nil)

	s := Session{Source: "", Tool: "claude", Session: "s1", State: "waiting", UpdatedAt: time.Now()}
	short := Snapshot{Now: time.Now(), Sessions: []Session{s}}
	pShort := RenderForCoord(short, s.Key(), 0, true, 30, nil)

	if text, _ := pShort["text"].(string); text != "WAIT" {
		t.Fatalf("text = %q, want WAIT", text)
	}
	want := map[string]any{"whenFits": "static"}
	for name, p := range map[string]map[string]any{"WAIT MBP": pLong, "WAIT": pShort} {
		got, ok := p["scroll"].(map[string]any)
		if !ok {
			t.Fatalf("%s: scroll = %v, want an object", name, p["scroll"])
		}
		if len(got) != len(want) || got["whenFits"] != want["whenFits"] {
			t.Errorf("%s: scroll = %v, want %v", name, got, want)
		}
	}
}
