package render

import (
	"testing"
	"time"
)

func nativeTextSession() Session {
	return Session{Source: "m4", Tool: "claude", Session: "s", State: "running",
		UpdatedAt: time.Now(), ContextPct: intPtr(40)}
}

// The 3×5 font has no room for a real M (or N, or W): three columns can't hold
// a middle peak plus two stems. The source name is the one card that is pure
// letters, so it hands the text to the firmware's own font instead of drawing
// it — the number slot stays unpainted in the bitmap and NativeText carries it.
func TestSourceCardLeavesTheTextSlotToTheFirmware(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	f := ComposeFrame(s, cardSource, nil, []Session{s}, now)

	if f.Native == nil {
		t.Fatal("source card: Native is nil, want the source name")
	}
	if f.Native.Text != "M4" {
		t.Errorf("Native.Text = %q, want %q", f.Native.Text, "M4")
	}
	if f.Native.X != numStart {
		t.Errorf("Native.X = %d, want numStart %d", f.Native.X, numStart)
	}
	for y := 0; y < 8; y++ {
		for x := numStart; x < glassLeft; x++ {
			if f.Dirty[y][x] && y != barRow {
				t.Errorf("text slot pixel (%d,%d) painted; the firmware owns this area now", x, y)
			}
		}
	}
}

// Everything else on the card still comes from the bitmap.
func TestSourceCardStillDrawsIconGlassAndBar(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	f := ComposeFrame(s, cardSource, nil, []Session{s}, now)

	if !f.Dirty[1][1] {
		t.Error("tool icon missing from the source card")
	}
	if f.Pixels[1][glassRight] != glassWall {
		t.Error("context glass missing from the source card")
	}
	if !f.Dirty[barRow][barStart] {
		t.Error("session bar missing from the source card")
	}
}

// The payload has to spell out textCenter:false — NG adds textOffsetX to the
// centred position otherwise, which walks the name off the right edge.
func TestSourceCardPayloadCarriesNativeText(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	snap := Snapshot{Now: now, Sessions: []Session{s}}

	p := RenderForCoord(snap, s.Key(), 0, false, 30, nil)
	if p == nil {
		t.Fatal("RenderForCoord returned nil")
	}
	if got := p["text"]; got != "M4" {
		t.Errorf("payload text = %v, want M4", got)
	}
	if got := p["textCenter"]; got != false {
		t.Errorf("payload textCenter = %v, want false", got)
	}
	if got := p["textOffsetX"]; got != numStart {
		t.Errorf("payload textOffsetX = %v, want %d", got, numStart)
	}
	if _, ok := p["textColor"]; !ok {
		t.Error("payload missing textColor")
	}
	if _, ok := p["draw"]; !ok {
		t.Error("payload missing draw (icon/glass/bar are still a bitmap)")
	}
}

// A card with no native text must not grow the text keys — an empty "text" on
// a bitmap-only frame would blank NG's own rendering of nothing in particular.
func TestNonSourceCardPayloadHasNoTextKeys(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	s.SourceCard = boolPtr(false)
	snap := Snapshot{Now: now, Sessions: []Session{s}}

	p := RenderForCoord(snap, s.Key(), 0, false, 30, nil)
	if p == nil {
		t.Fatal("RenderForCoord returned nil")
	}
	if _, ok := p["text"]; ok {
		t.Errorf("payload carries text on a bitmap-only card: %v", p["text"])
	}
	if _, ok := p["textOffsetX"]; ok {
		t.Error("payload carries textOffsetX on a bitmap-only card")
	}
}

// The Settings preview renders the bitmap, so a card whose text is native
// would otherwise show an empty slot. It draws the 3×5 approximation instead —
// same content, near-enough letterforms — rather than lying about a blank card.
func TestPreviewFillsInNativeTextWithTheBitmapFont(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	p := PreviewFrames(s, nil, now)

	var source *CardFrame
	for i := range p.Frames {
		if p.Frames[i].Card == "source" {
			source = &p.Frames[i]
		}
	}
	if source == nil {
		t.Fatal("preview has no source card")
	}
	lit := 0
	for y := 0; y < 8; y++ {
		for x := numStart; x < glassLeft; x++ {
			if source.Pixels[y*32+x] != "#000000" {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Error("preview source card has an empty text slot; it must approximate the native text")
	}
}

func boolPtr(v bool) *bool { return &v }

// A single draw op covering the whole panel suppresses NG's text layer outright
// (verified on firmware 1.0.15), so a frame carrying native text must emit its
// bitmap as blocks that leave the text box clear — while still painting the bar
// row underneath it.
func TestNativeTextPayloadLeavesTheTextBoxUndrawn(t *testing.T) {
	now := time.Now()
	s := nativeTextSession()
	snap := Snapshot{Now: now, Sessions: []Session{s}}

	p := RenderForCoord(snap, s.Key(), 0, false, 30, nil)
	ops, ok := p["draw"].([]any)
	if !ok {
		t.Fatalf("draw is %T, want []any", p["draw"])
	}
	if len(ops) < 2 {
		t.Fatalf("draw has %d op(s); a full-panel bitmap would hide the text", len(ops))
	}
	sawBarRow := false
	for _, raw := range ops {
		op := raw.([]any)
		x, y := op[1].(int), op[2].(int)
		w, h := op[3].(int), op[4].(int)
		if y == barRow && h == 1 {
			sawBarRow = true
			continue
		}
		// Every other op must clear the text columns entirely.
		if x < glassLeft && x+w > numStart && y < barRow {
			t.Errorf("draw op x=%d w=%d y=%d h=%d overlaps the native text box (cols %d-%d)",
				x, w, y, h, numStart, glassLeft-1)
		}
	}
	if !sawBarRow {
		t.Error("no draw op covers the bar row under the text; the session bar would vanish")
	}
}
