package render

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSampleBaseSession(t *testing.T) {
	s := SampleBaseSession()
	if s.Source != "mbp" || s.Tool != "claude" || s.Session != "sample" || s.State != "running" {
		t.Fatalf("unexpected sample base: %+v", s)
	}
}

func TestPreviewSessionToggles(t *testing.T) {
	base := Session{Source: "mbp", Tool: "claude", State: "running"}

	off := PreviewSession(DraftDisplay{}, base)
	if off.ContextPct != nil || off.RateWindowPct != nil {
		t.Fatal("pct fields should be nil when toggles off")
	}
	if off.Activity != "" {
		t.Fatal("activity should be empty when off")
	}
	if off.SourceColor != nil {
		t.Fatal("source color should be nil when empty")
	}

	on := PreviewSession(DraftDisplay{
		ContextPct: true, RateBottomBar: true, ActivityDetail: true,
		SourceColor: "#ff8800",
	}, base)
	if on.ContextPct == nil || *on.ContextPct != samplePct {
		t.Fatalf("ctx pct sample = %v", on.ContextPct)
	}
	if on.RateWindowPct == nil || *on.RateWindowPct != samplePct {
		t.Fatalf("rate pct sample = %v", on.RateWindowPct)
	}
	if !on.RateBottomBar {
		t.Fatal("bool fields should pass through")
	}
	if on.Activity != sampleActivity {
		t.Fatalf("activity sample = %q", on.Activity)
	}
	if on.SourceColor == nil || *on.SourceColor != "#ff8800" {
		t.Fatal("source color should be set")
	}

	live := base
	live.ContextPct = ptrInt(10)
	got := PreviewSession(DraftDisplay{ContextPct: true}, live)
	if got.ContextPct == nil || *got.ContextPct != 10 {
		t.Fatalf("live ctx pct should win over sample, got %v", got.ContextPct)
	}

	// Live-value leakage: RateWindowPct must be nil when RateBottomBar is off,
	// even if the base session carries a live value.
	liveRate := base
	liveRate.RateWindowPct = ptrInt(75)
	leaked := PreviewSession(DraftDisplay{RateBottomBar: false}, liveRate)
	if leaked.RateWindowPct != nil {
		t.Fatalf("live RateWindowPct leaked through disabled RateBottomBar: %v", leaked.RateWindowPct)
	}
}

func TestPreviewSessionSlimDraft(t *testing.T) {
	base := Session{Source: "mbp", Tool: "claude", State: "running"}

	off := PreviewSession(DraftDisplay{}, base)
	if off.ContextPct != nil || off.RateWindowPct != nil || off.Activity != "" {
		t.Fatalf("all-off draft leaked data: %+v", off)
	}

	on := PreviewSession(DraftDisplay{ContextPct: true, RateBottomBar: true, ActivityDetail: true}, base)
	if on.ContextPct == nil || *on.ContextPct != samplePct {
		t.Fatal("context sample missing")
	}
	if !on.RateBottomBar || on.RateWindowPct == nil {
		t.Fatal("rate-bar mode must seed sample rate data for the bar")
	}
	if on.Activity == "" {
		t.Fatal("activity sample missing")
	}
}

func TestPreviewFramesIncludeUsageCards(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	s := PreviewSession(DraftDisplay{SourceCard: true, RateBottomBar: true}, SampleBaseSession())
	p := PreviewFrames(s, SampleUsageView(), now)
	names := map[string]bool{}
	for _, f := range p.Frames {
		names[f.Card] = true
	}
	for _, want := range []string{"source", "usage-5h", "usage-7d"} {
		if !names[want] {
			t.Errorf("missing preview card %q (got %v)", want, names)
		}
	}
	// Without a view: no usage cards.
	p = PreviewFrames(s, nil, now)
	for _, f := range p.Frames {
		if strings.HasPrefix(f.Card, "usage-") {
			t.Errorf("nil view rendered usage card %q", f.Card)
		}
	}
}

func TestPreviewFramesExcludesToolCard(t *testing.T) {
	// PreviewFrames uses AvailableCards(s, nil): no usage view, so only
	// source and tool are possible. The tool card is excluded from Frames
	// (it has no static grid form) and reflected in Activity instead.
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	s := Session{
		Source: "mbp", Tool: "claude", State: "running", Activity: "Bash: go test",
	}
	p := PreviewFrames(s, nil, now)

	if p.Width != 32 || p.Height != 8 {
		t.Fatalf("dims = %dx%d", p.Width, p.Height)
	}
	var names []string
	for _, f := range p.Frames {
		names = append(names, f.Card)
		if len(f.Pixels) != 256 {
			t.Fatalf("card %s pixels = %d, want 256", f.Card, len(f.Pixels))
		}
		for _, px := range f.Pixels {
			if len(px) != 7 || px[0] != '#' {
				t.Fatalf("bad hex pixel %q", px)
			}
		}
	}
	// AvailableCards(s, nil): source + tool; tool is excluded from Frames.
	if want := []string{"source"}; !slices.Equal(names, want) {
		t.Fatalf("cards = %v, want %v", names, want)
	}
	if p.Activity != "Bash: go test" {
		t.Fatalf("activity = %q", p.Activity)
	}
}

func TestPreviewFramesBareSession(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	p := PreviewFrames(Session{Source: "mbp", Tool: "claude", State: "running"}, nil, now)
	if len(p.Frames) != 1 || p.Frames[0].Card != "source" {
		t.Fatalf("frames = %+v", p.Frames)
	}
	if p.Activity != "" {
		t.Fatalf("activity = %q, want empty", p.Activity)
	}
}
