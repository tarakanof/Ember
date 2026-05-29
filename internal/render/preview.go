package render

import "time"

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
// by PreviewSession plus an optional source colour. (STATUS_ACTIVITY_TRAIL is
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
