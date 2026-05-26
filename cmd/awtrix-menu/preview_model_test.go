package main

import (
	"testing"
	"time"

	"github.com/dt/awtrix-ai-status/internal/render"
)

func intp(v int) *int { return &v }

func TestPreviewSessionFormDecidesElements(t *testing.T) {
	// Base session carries NO values (simulating an unreachable service).
	base := render.Session{Source: "mbp", Tool: "claude", Session: "s1", State: "running"}
	f := settingsForm{
		ContextPct:     true, // glass on
		RatePct:        true, // rate card on
		ContextNumber:  true, // context card on
		RateReset:      true, // reset card on
		RateBottomBar:  true, // bottom bar = rate
		ActivityDetail: true, // tool card on
		SourceColor:    "#aa66ff",
	}
	s := previewSession(f, base)

	if s.ContextPct == nil {
		t.Error("ContextPct should be set (glass on) with a sample value")
	}
	if s.RateWindowPct == nil {
		t.Error("RateWindowPct should be set (rate on) with a sample value")
	}
	if !s.ContextNumber || !s.RateReset || !s.RateBottomBar {
		t.Error("flag fields must mirror the form")
	}
	if s.RateResetAt == 0 {
		t.Error("RateResetAt should get a sample future value when reset on")
	}
	if s.Activity == "" {
		t.Error("Activity should be set when detail on")
	}
	if s.SourceColor == nil || *s.SourceColor != "#aa66ff" {
		t.Errorf("SourceColor = %v, want #aa66ff", s.SourceColor)
	}
}

func TestPreviewSessionDisabledElementsCleared(t *testing.T) {
	base := render.Session{
		Source: "mbp", Tool: "claude", Session: "s1", State: "running",
		ContextPct: intp(73), RateWindowPct: intp(55), RateResetAt: 9_999_999_999,
		Activity: "Bash", ContextNumber: true, RateBottomBar: true, RateReset: true,
	}
	f := settingsForm{} // all toggles off, no colour
	s := previewSession(f, base)

	if s.ContextPct != nil {
		t.Error("glass off -> ContextPct must be nil")
	}
	if s.RateWindowPct != nil {
		t.Error("rate off -> RateWindowPct must be nil")
	}
	if s.ContextNumber || s.RateBottomBar || s.RateReset {
		t.Error("flag fields must all be false")
	}
	if s.Activity != "" {
		t.Error("detail off -> Activity must be empty")
	}
	if s.RateResetAt != 0 {
		t.Error("reset off -> RateResetAt must be 0")
	}
	if s.SourceColor != nil {
		t.Error("blank colour -> SourceColor must be nil")
	}
}

func TestPreviewSessionPrefersLiveValues(t *testing.T) {
	base := render.Session{State: "running", ContextPct: intp(73), RateWindowPct: intp(55)}
	f := settingsForm{ContextPct: true, RatePct: true}
	s := previewSession(f, base)
	if s.ContextPct == nil || *s.ContextPct != 73 {
		t.Errorf("ContextPct = %v, want live 73", s.ContextPct)
	}
	if s.RateWindowPct == nil || *s.RateWindowPct != 55 {
		t.Errorf("RateWindowPct = %v, want live 55", s.RateWindowPct)
	}
}

func TestCursorForCard(t *testing.T) {
	cards := []int{cardXY, cardRate, cardCtx} // indices 0, 1, 2
	if got := cursorForCard(cards, cardRate); got != 1 {
		t.Errorf("cursorForCard(cardRate) = %d, want 1", got)
	}
	if got := cursorForCard(cards, cardCtx); got != 2 {
		t.Errorf("cursorForCard(cardCtx) = %d, want 2", got)
	}
	// A card that isn't available falls back to the X/Y card at index 0.
	if got := cursorForCard(cards, cardReset); got != 0 {
		t.Errorf("cursorForCard(absent) = %d, want 0", got)
	}
}

// TestFocusedNumberSlotCardDiffersFromXY is the regression guard for the bug
// where toggling a number-slot element re-rendered the X/Y card (which never
// shows that element), so the toggle looked like a no-op. focusCard seeks the
// element's own card; here we assert each value card renders a visibly
// different frame from the X/Y card, so the toggle is observable.
//
// The tool card is the exception: its content is scrolling text drawn via the
// device's detail payload, not a static bitmap, so ComposeFrame renders it like
// the X/Y card. Its preview feedback is the caption, so we only assert that
// focusing it actually lands on the tool card (which swaps the caption).
func TestFocusedNumberSlotCardDiffersFromXY(t *testing.T) {
	base := sampleBaseSession()
	allOn := settingsForm{
		ContextPct: true, RatePct: true, RateReset: true,
		ActivityDetail: true, ActivityTrail: true, ContextNumber: true,
		RateBottomBar: true, SourceColor: "#aa66ff",
	}
	robot := render.RGB{R: 0xaa, G: 0x66, B: 0xff}
	s := previewSession(allOn, base)
	cards := render.AvailableCards(s)
	frameAt := func(card int) render.Frame {
		shown := cards[cursorForCard(cards, card)]
		return render.ComposeFrame(s, 1, 1, shown, robot, []render.Session{s}, time.Unix(0, 0))
	}
	xy := frameAt(cardXY)
	for _, tc := range []struct {
		name string
		card int
	}{
		{"Context %", cardCtx},
		{"5h rate-limit %", cardRate},
		{"Reset countdown", cardReset},
	} {
		if frameAt(tc.card) == xy {
			t.Errorf("%s card renders identically to the X/Y card; toggling it would be invisible in the preview", tc.name)
		}
	}
	// Tool card: assert the rotation lands on it (drives the caption change).
	if got := cards[cursorForCard(cards, cardTool)]; got != cardTool {
		t.Errorf("focusing the tool card selected %d, want cardTool=%d; the caption would not update", got, cardTool)
	}
}
