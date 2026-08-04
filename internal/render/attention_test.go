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
