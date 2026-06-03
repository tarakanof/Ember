package render

import (
	"fmt"
	"time"
)

// Sample values used when a draft toggle is enabled but the live base session
// has no value to show (service unreachable, or the producer wasn't sending
// that field yet). Chosen to look representative on the preview.
const (
	samplePct      = 47
	sampleResetHrs = 3
	sampleActivity = "Bash: npm test"
)

// SampleBaseSession is the placeholder shown when /state is unreachable or
// empty. Lifted from the old menu's state_fetch.go so /v1/preview reproduces
// the previous offline preview.
func SampleBaseSession() Session {
	return Session{Source: "mbp", Tool: "claude", Session: "sample", State: "running"}
}

// ptrInt returns a pointer to v, for Session optional-int fields.
func ptrInt(v int) *int { return &v }

// DraftDisplay is the set of display toggles the Settings "Display" tab edits
// that affect a single-session render. It is the 6 effective booleans consumed
// by PreviewSession plus an optional source colour. (EMBER_ACTIVITY_TRAIL is
// intentionally absent: it affects the multi-session bar, not one session.)
type DraftDisplay struct {
	ContextPct     bool
	RatePct        bool
	RateReset      bool
	ContextNumber  bool
	RateBottomBar  bool
	ActivityDetail bool
	SourceColor    string // "" = no tint
}

// PreviewSession applies a draft to a base session and returns the Session the
// renderer should draw. Live values are preferred; sample fallbacks ensure an
// enabled element is never blank. now seeds the rate-reset sample so the reset
// card renders ~sampleResetHrs ahead. Ported from the old menu's previewSession.
func PreviewSession(d DraftDisplay, base Session, now time.Time) Session {
	s := base

	if d.ContextPct {
		if s.ContextPct == nil {
			s.ContextPct = ptrInt(samplePct)
		}
	} else {
		s.ContextPct = nil
	}

	if d.RatePct {
		if s.RateWindowPct == nil {
			s.RateWindowPct = ptrInt(samplePct)
		}
	} else {
		s.RateWindowPct = nil
	}

	s.RateReset = d.RateReset
	if d.RateReset {
		if s.RateResetAt == 0 {
			s.RateResetAt = now.Add(sampleResetHrs * time.Hour).Unix()
		}
	} else {
		s.RateResetAt = 0
	}

	s.ContextNumber = d.ContextNumber
	s.RateBottomBar = d.RateBottomBar

	if d.ActivityDetail {
		if s.Activity == "" {
			s.Activity = sampleActivity
		}
	} else {
		s.Activity = ""
	}

	if d.SourceColor != "" {
		c := d.SourceColor
		s.SourceColor = &c
	} else {
		s.SourceColor = nil
	}

	return s
}

// CardFrame is one rendered rotation card as a 32x8 grid of row-major
// "#rrggbb" strings (256 entries). Undirty (off) pixels are "#000000".
type CardFrame struct {
	Card   string   `json:"card"`
	Pixels []string `json:"pixels"`
}

// Preview is the /v1/preview response: every renderable card for the session
// plus the activity string for the scrolling text/tool card (which has no
// static grid form, so it is not included in Frames).
type Preview struct {
	Width    int         `json:"width"`
	Height   int         `json:"height"`
	Activity string      `json:"activity"`
	Frames   []CardFrame `json:"frames"`
}

// PreviewFrames renders each card in AvailableCards(s) except the scrolling
// tool card, using the established single-session preview call
// (idx=1,total=1, robot colour from state, bottom bar fed the single session).
func PreviewFrames(s Session, now time.Time) Preview {
	p := Preview{Width: 32, Height: 8, Frames: []CardFrame{}}
	for _, c := range AvailableCards(s) {
		if c == cardTool {
			p.Activity = s.Activity
			continue
		}
		frame := ComposeFrame(s, 1, 1, c, colorForState(s.State), []Session{s}, now)
		p.Frames = append(p.Frames, CardFrame{Card: cardName(c), Pixels: hexPixels(&frame)})
	}
	return p
}

func cardName(c int) string {
	switch c {
	case cardXY:
		return "xy"
	case cardRate:
		return "rate"
	case cardTool:
		return "tool"
	case cardCtx:
		return "ctx"
	case cardReset:
		return "reset"
	default:
		panic(fmt.Sprintf("cardName: unknown card const %d", c))
	}
}

func hexPixels(f *Frame) []string {
	ints := framePixels(f)
	out := make([]string, len(ints))
	for i, v := range ints {
		out[i] = fmt.Sprintf("#%06x", v)
	}
	return out
}
