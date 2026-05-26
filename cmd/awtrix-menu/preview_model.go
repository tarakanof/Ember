package main

import (
	"time"

	"github.com/dt/awtrix-ai-status/internal/render"
)

// Sample values used when a toggle is enabled but the live base session has no
// value to show (service unreachable, or the producer wasn't sending that
// field yet). Chosen to look representative on the preview.
const (
	samplePct      = 47
	sampleResetHrs = 3
	sampleActivity = "Bash: npm test"
)

// Card indices mirror internal/render's unexported card iota (cardXY=0,
// cardRate=1, cardTool=2, cardCtx=3, cardReset=4). The menu uses them to focus
// the preview rotation on the card a just-toggled checkbox controls.
const (
	cardXY = iota
	cardRate
	cardTool
	cardCtx
	cardReset
)

// cursorForCard returns the rotation index in cards that shows card, or 0 (the
// always-present X/Y card) when card isn't currently available — e.g. the
// element was just disabled, so its card is gone from the rotation.
func cursorForCard(cards []int, card int) int {
	for i, c := range cards {
		if c == card {
			return i
		}
	}
	return 0
}

// previewSession applies the current form to a base session (from /state or a
// sample) and returns the Session the renderer should draw. The form decides
// which elements appear; live values are preferred, with sample fallbacks so an
// enabled element is never blank.
func previewSession(f settingsForm, base render.Session) render.Session {
	s := base

	if f.ContextPct {
		if s.ContextPct == nil {
			s.ContextPct = ptr(samplePct)
		}
	} else {
		s.ContextPct = nil
	}

	if f.RatePct {
		if s.RateWindowPct == nil {
			s.RateWindowPct = ptr(samplePct)
		}
	} else {
		s.RateWindowPct = nil
	}

	s.RateReset = f.RateReset
	if f.RateReset {
		if s.RateResetAt == 0 {
			s.RateResetAt = time.Now().Add(sampleResetHrs * time.Hour).Unix()
		}
	} else {
		s.RateResetAt = 0
	}

	s.ContextNumber = f.ContextNumber
	s.RateBottomBar = f.RateBottomBar

	if f.ActivityDetail {
		if s.Activity == "" {
			s.Activity = sampleActivity
		}
	} else {
		s.Activity = ""
	}

	if f.SourceColor != "" {
		c := f.SourceColor
		s.SourceColor = &c
	} else {
		s.SourceColor = nil
	}

	return s
}

func ptr(v int) *int { return &v }
