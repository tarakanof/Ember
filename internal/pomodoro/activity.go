package pomodoro

import (
	"slices"
	"time"
)

// ActivityTotals is an AI-coding-activity rollup for one group (a tool, a
// source, or everything) over a window of heartbeats.
type ActivityTotals struct {
	// ActiveSec is wall-clock active time: each session's heartbeats are merged
	// into spans (see activitySpans), and the spans of all sessions in the group
	// are unioned per logical day, so two concurrent sessions don't count twice.
	ActiveSec int
	// Sessions is the number of distinct session keys seen.
	Sessions int
	// Attention is the number of waiting episodes: a run of consecutive
	// "waiting" heartbeats within one session counts once.
	Attention int
}

// SummarizeActivity rolls heartbeats up per group(a). Heartbeats no more than
// spanGap apart within a session form one active span; days are logical days
// per dayStartHour in loc, the same bucketing as the work-hours view.
func SummarizeActivity(acts []ActivityRecord, group func(ActivityRecord) string, spanGap time.Duration, dayStartHour int, loc *time.Location) map[string]ActivityTotals {
	out := map[string]ActivityTotals{}
	for _, perGroup := range DailyActiveSec(acts, group, spanGap, dayStartHour, loc) {
		for g, secs := range perGroup {
			t := out[g]
			t.ActiveSec += secs
			out[g] = t
		}
	}

	sorted := slices.Clone(acts)
	slices.SortStableFunc(sorted, func(a, b ActivityRecord) int { return a.At.Compare(b.At) })
	sessions := map[string]map[string]bool{} // group → session keys
	lastState := map[string]string{}         // session key → previous state
	for _, a := range sorted {
		g := group(a)
		if sessions[g] == nil {
			sessions[g] = map[string]bool{}
		}
		sessions[g][a.SessionKey] = true
		if a.State == "waiting" && lastState[a.SessionKey] != "waiting" {
			t := out[g]
			t.Attention++
			out[g] = t
		}
		lastState[a.SessionKey] = a.State
	}
	for g, keys := range sessions {
		t := out[g]
		t.Sessions = len(keys)
		out[g] = t
	}
	return out
}

// DailyActiveSec returns wall-clock active seconds keyed by logical day
// ("2006-01-02") and then by group(a). Only days and groups with heartbeats
// appear.
func DailyActiveSec(acts []ActivityRecord, group func(ActivityRecord) string, spanGap time.Duration, dayStartHour int, loc *time.Location) map[string]map[string]int {
	type bucket struct{ day, group, session string }
	bySession := map[bucket][]ActivityRecord{}
	for _, a := range acts {
		k := bucket{dayKey(a.At, dayStartHour, loc), group(a), a.SessionKey}
		bySession[k] = append(bySession[k], a)
	}

	type dayGroup struct{ day, group string }
	spans := map[dayGroup][]Interval{}
	for k, recs := range bySession {
		dg := dayGroup{k.day, k.group}
		spans[dg] = append(spans[dg], activitySpans(recs, spanGap)...)
	}

	out := map[string]map[string]int{}
	for dg, ivs := range spans {
		if out[dg.day] == nil {
			out[dg.day] = map[string]int{}
		}
		out[dg.day][dg.group] = totalSec(mergeIntervals(ivs, 0))
	}
	return out
}
