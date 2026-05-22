package main

import (
	"encoding/json"
	"math"
)

// statuslineInput is the subset of Claude Code's statusline JSON we read.
type statuslineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
		} `json:"five_hour"`
	} `json:"rate_limits"`
}

func parseStatusline(b []byte) (statuslineInput, bool) {
	var in statuslineInput
	if json.Unmarshal(b, &in) != nil {
		return statuslineInput{}, false
	}
	return in, true
}

// extractRatePct returns the 5h rate-limit used-percentage as a clamped int
// (0..100), or (nil,false) when rate_limits.five_hour is absent.
func extractRatePct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil {
		return nil, false
	}
	pct := int(math.Round(in.RateLimits.FiveHour.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

// enrichMarkerRate merges rate_window_pct into an EXISTING session marker,
// preserving all hook-set fields. All checks happen inside the same flock the
// hooks take. Enrich-only: absent marker (hooks own creation) or unparseable
// marker (don't clobber hook data) → left untouched.
func enrichMarkerRate(stateDir, sessionID string, pct int) error {
	mp := markerPath(stateDir, sessionID)
	lp := lockPath(stateDir, sessionID)
	return withLockEx(lp, func() error {
		body, err := readMarker(mp)
		if err != nil {
			return nil // absent/unreadable → skip
		}
		var req StatusRequest
		if json.Unmarshal(body, &req) != nil {
			return nil // unparseable → skip (data-loss guard)
		}
		p := pct
		req.RateWindowPct = &p
		out, err := json.Marshal(req)
		if err != nil {
			return nil
		}
		return writeMarker(mp, out)
	})
}
