package render

import (
	"testing"
	"time"
)

func bptr(b bool) *bool { return &b }

func TestSourceCardAvailability(t *testing.T) {
	base := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running"}

	// Default (nil pointer): source card present and first.
	cards := AvailableCards(base, nil)
	if len(cards) == 0 || cards[0] != cardSource {
		t.Fatalf("default cards = %v, want cardSource first", cards)
	}

	// Explicitly disabled: absent.
	off := base
	off.SourceCard = bptr(false)
	for _, c := range AvailableCards(off, nil) {
		if c == cardSource {
			t.Fatal("cardSource present despite source_card=false")
		}
	}

	// Empty source: absent even when enabled.
	noSrc := base
	noSrc.Source = ""
	for _, c := range AvailableCards(noSrc, nil) {
		if c == cardSource {
			t.Fatal("cardSource present despite empty source")
		}
	}
}

func TestAvailableCardsMayBeEmpty(t *testing.T) {
	s := Session{Source: "", Tool: "claude", Session: "s1", State: "done"}
	if got := AvailableCards(s, nil); len(got) != 0 {
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
		"über-mac": "ÜBER", // multibyte: rune (not byte) truncation to 4 runes
	} {
		if got := sourceCardText(in); got != want {
			t.Fatalf("sourceCardText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeFrameSourceCard(t *testing.T) {
	col := "#3366FF"
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", SourceColor: &col}
	f := ComposeFrame(s, cardSource, []Session{s}, time.Now())
	// 'M' glyph top-left pixel at numStart, row 1, in the source colour.
	want := RGB{0x33, 0x66, 0xFF}
	if !f.Dirty[1][numStart] || f.Pixels[1][numStart] != want {
		t.Fatalf("source-card first pixel = %v dirty=%v, want %v", f.Pixels[1][numStart], f.Dirty[1][numStart], want)
	}
}

func TestComposeFrameNoCardBlankNumberSlot(t *testing.T) {
	// source_card=false + non-empty Source: card -1 must draw NOTHING in the
	// number slot (cols 9-23, rows 1-5) — regression for the review finding.
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", SourceCard: bptr(false)}
	f := ComposeFrame(s, cardNone, []Session{s}, time.Now())
	for y := 1; y <= 5; y++ {
		for x := numStart; x <= 23; x++ {
			if f.Dirty[y][x] {
				t.Fatalf("pixel (%d,%d) lit; number slot must be blank", x, y)
			}
		}
	}
}

func TestComposeFrameBottomBarModes(t *testing.T) {
	pct := 50
	s := Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running", RateWindowPct: &pct}

	// Default: session bar (one running pixel at barStart).
	f := ComposeFrame(s, cardSource, []Session{s}, time.Now())
	if !f.Dirty[barRow][barStart] {
		t.Fatal("expected session bar pixel at default settings")
	}

	// session_bar=false, no rate bar: row 7 empty.
	off := s
	off.SessionBar = bptr(false)
	f = ComposeFrame(off, cardSource, []Session{off}, time.Now())
	for x := 0; x < 32; x++ {
		if f.Dirty[barRow][x] {
			t.Fatalf("row 7 pixel %d lit with bar mode off", x)
		}
	}

	// rate bar wins regardless of session_bar.
	rate := off
	rate.RateBottomBar = true
	f = ComposeFrame(rate, cardSource, []Session{rate}, time.Now())
	if !f.Dirty[barRow][8] {
		t.Fatal("expected rate bar at col 8")
	}
}
