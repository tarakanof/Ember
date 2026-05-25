package main

import (
	"testing"

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
