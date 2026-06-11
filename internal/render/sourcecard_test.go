package render

import (
	"testing"
	"time"
)

func bptr(b bool) *bool { return &b }

func TestSourceCardAvailability(t *testing.T) {
	base := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running"}

	// Default (nil pointer): source card present and first.
	cards := AvailableCards(base)
	if len(cards) == 0 || cards[0] != cardSource {
		t.Fatalf("default cards = %v, want cardSource first", cards)
	}

	// Explicitly disabled: absent.
	off := base
	off.SourceCard = bptr(false)
	for _, c := range AvailableCards(off) {
		if c == cardSource {
			t.Fatal("cardSource present despite source_card=false")
		}
	}

	// Empty source: absent even when enabled.
	noSrc := base
	noSrc.Source = ""
	for _, c := range AvailableCards(noSrc) {
		if c == cardSource {
			t.Fatal("cardSource present despite empty source")
		}
	}
}

func TestAvailableCardsMayBeEmpty(t *testing.T) {
	s := Session{Source: "", Tool: "claude", Session: "s1", State: "done"}
	if got := AvailableCards(s); len(got) != 0 {
		t.Fatalf("cards = %v, want empty", got)
	}
}

func TestRenderForCoordNoCardsDoesNotPanic(t *testing.T) {
	// A session that yields zero cards (no source, no data) must render a
	// frame (icon/bar only), not panic on an empty card slice.
	s := Session{Source: "", Tool: "claude", Session: "s1", State: "done", UpdatedAt: time.Now()}
	snap := Snapshot{Now: time.Now(), Sessions: []Session{s}}
	p := RenderForCoord(snap, s.Key(), 0, false, 30)
	if p == nil {
		t.Fatal("expected a payload for an active (done) session")
	}
}

func TestSourceCardText(t *testing.T) {
	for in, want := range map[string]string{
		"mbp": "MBP", "studio-m4": "STUD", "": "",
	} {
		if got := sourceCardText(in); got != want {
			t.Fatalf("sourceCardText(%q) = %q, want %q", in, got, want)
		}
	}
}
