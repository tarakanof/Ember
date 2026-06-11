package render

import "testing"

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

func TestSourceCardText(t *testing.T) {
	for in, want := range map[string]string{
		"mbp": "MBP", "studio-m4": "STUD", "": "",
	} {
		if got := sourceCardText(in); got != want {
			t.Fatalf("sourceCardText(%q) = %q, want %q", in, got, want)
		}
	}
}
