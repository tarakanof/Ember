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
	sampleActivity = "Bash: npm test"
)

// SampleBaseSession is the placeholder shown when /state is unreachable or
// empty. Lifted from the old menu's state_fetch.go so /v1/preview reproduces
// the previous offline preview.
func SampleBaseSession() Session {
	return Session{Source: "mbp", Tool: "claude", Session: "sample", State: "running"}
}

// SampleUsageView is the representative usage card shown in the preview
// (87% — amber, over the default threshold — resetting 17:30; 7d at 42%).
func SampleUsageView() *UsageView {
	p := 42
	return &UsageView{FiveHourPct: 87, ResetLabel: "17:30", SevenDayPct: &p}
}

// ptrInt returns a pointer to v, for Session optional-int fields.
func ptrInt(v int) *int { return &v }

// DraftDisplay is the set of display toggles the Settings "Agent" pane edits
// that affect a single-session render. (Activity trail is intentionally
// absent: it affects the multi-session bar, not one session. The usage card
// is driven separately — the handler passes a sample UsageView.)
type DraftDisplay struct {
	ContextPct     bool
	RateBottomBar  bool
	ActivityDetail bool
	SourceCard     bool
	SessionBar     bool
	SourceColor    string // "" = no tint
}

// PreviewSession applies draft toggles to a base session and returns the
// Session the renderer should draw. Live values are preferred; sample fallbacks
// ensure an enabled element is never blank. Ported from the old menu's
// previewSession.
func PreviewSession(d DraftDisplay, base Session) Session {
	s := base

	if d.ContextPct {
		if s.ContextPct == nil {
			s.ContextPct = ptrInt(samplePct)
		}
	} else {
		s.ContextPct = nil
	}

	if d.RateBottomBar {
		if s.RateWindowPct == nil {
			s.RateWindowPct = ptrInt(samplePct)
		}
	} else {
		s.RateWindowPct = nil
	}
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

	s.SourceCard = &d.SourceCard
	s.SessionBar = &d.SessionBar

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

// HexPixels exports a frame as the row-major "#rrggbb" strings preview JSON
// consumers expect (see CardFrame.Pixels). It renders a frame to "#rrggbb" strings, with any firmware-rendered
// text approximated in the bitmap font (see withNativeApproximated) — a preview
// can only show pixels, and an empty slot would misrepresent the card.
func HexPixels(f *Frame) []string {
	approx := withNativeApproximated(f)
	return hexPixels(&approx)
}

// PreviewFrames renders each card in AvailableCards(s, u) except the
// scrolling tool card, using the robot colour from state and the single
// session as the bottom-bar source. Pass a non-nil UsageView to include
// usage faces in the preview.
func PreviewFrames(s Session, u *UsageView, now time.Time) Preview {
	p := Preview{Width: 32, Height: 8, Frames: []CardFrame{}}
	for _, c := range AvailableCards(s, u) {
		if c == cardTool {
			p.Activity = s.Activity
			continue
		}
		frame := ComposeFrame(s, c, u, []Session{s}, now)
		p.Frames = append(p.Frames, CardFrame{Card: cardName(c), Pixels: HexPixels(&frame)})
	}
	return p
}

func cardName(c int) string {
	switch c {
	case cardSource:
		return "source"
	case cardTool:
		return "tool"
	case cardUsage5h:
		return "usage-5h"
	case cardUsageReset:
		return "usage-reset"
	case cardUsage7d:
		return "usage-7d"
	case cardUsageModelA:
		return "usage-model-a"
	case cardUsageModelB:
		return "usage-model-b"
	default:
		panic(fmt.Sprintf("cardName: unknown card const %d", c))
	}
}

// withNativeApproximated returns a copy of f with any firmware-rendered text
// painted in the 3×5 bitmap font. The preview can only show pixels, so a card
// whose text is native would otherwise render as an empty slot — a lie about a
// card that reads fine on the device. The letterforms differ slightly (that is
// the whole point of handing them to the firmware); the content does not.
func withNativeApproximated(f *Frame) Frame {
	out := *f
	if n := f.Native; n != nil && n.Text != "" {
		drawDigits(&out, n.Text, n.X, 1, n.Color)
	}
	return out
}

func hexPixels(f *Frame) []string {
	ints := framePixels(f)
	out := make([]string, len(ints))
	for i, v := range ints {
		out[i] = fmt.Sprintf("#%06x", v)
	}
	return out
}
