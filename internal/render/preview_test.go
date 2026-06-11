package render

import (
	"slices"
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
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	base := Session{Source: "mbp", Tool: "claude", State: "running"}

	off := PreviewSession(DraftDisplay{}, base, now)
	if off.ContextPct != nil || off.RateWindowPct != nil {
		t.Fatal("pct fields should be nil when toggles off")
	}
	if off.RateResetAt != 0 || off.RateReset {
		t.Fatal("reset should be cleared when off")
	}
	if off.Activity != "" {
		t.Fatal("activity should be empty when off")
	}
	if off.SourceColor != nil {
		t.Fatal("source color should be nil when empty")
	}

	on := PreviewSession(DraftDisplay{
		ContextPct: true, RatePct: true, RateReset: true,
		ContextNumber: true, RateBottomBar: true, ActivityDetail: true,
		SourceColor: "#ff8800",
	}, base, now)
	if on.ContextPct == nil || *on.ContextPct != samplePct {
		t.Fatalf("ctx pct sample = %v", on.ContextPct)
	}
	if on.RateWindowPct == nil || *on.RateWindowPct != samplePct {
		t.Fatalf("rate pct sample = %v", on.RateWindowPct)
	}
	if want := now.Add(sampleResetHrs * time.Hour).Unix(); on.RateResetAt != want {
		t.Fatalf("reset at = %d want %d", on.RateResetAt, want)
	}
	if !on.RateReset || !on.ContextNumber || !on.RateBottomBar {
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
	got := PreviewSession(DraftDisplay{ContextPct: true}, live, now)
	if got.ContextPct == nil || *got.ContextPct != 10 {
		t.Fatalf("live ctx pct should win over sample, got %v", got.ContextPct)
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
	p := PreviewFrames(s, now)

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
	p := PreviewFrames(Session{Source: "mbp", Tool: "claude", State: "running"}, now)
	if len(p.Frames) != 1 || p.Frames[0].Card != "source" {
		t.Fatalf("frames = %+v", p.Frames)
	}
	if p.Activity != "" {
		t.Fatalf("activity = %q, want empty", p.Activity)
	}
}
