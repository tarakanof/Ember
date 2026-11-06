package pomodoro

import (
	"slices"
	"time"
)

type ActivityTotals struct {
	ActiveSec int
	Sessions  int
	Attention int
}

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
	sessions := map[string]map[string]bool{}
	lastState := map[string]string{}
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

func workingState(state string) bool { return state == "running" || state == "error" }

func DailyActivity(acts []ActivityRecord, group func(ActivityRecord) string, spanGap time.Duration, dayStartHour int, loc *time.Location) map[string]map[string]ActivityTotals {
	byDay := map[string][]ActivityRecord{}
	for _, a := range acts {
		k := dayKey(a.At, dayStartHour, loc)
		byDay[k] = append(byDay[k], a)
	}
	out := make(map[string]map[string]ActivityTotals, len(byDay))
	for day, recs := range byDay {
		out[day] = SummarizeActivity(recs, group, spanGap, dayStartHour, loc)
	}
	return out
}

func DailyActiveSec(acts []ActivityRecord, group func(ActivityRecord) string, spanGap time.Duration, dayStartHour int, loc *time.Location) map[string]map[string]int {
	type bucket struct{ day, group, session string }
	bySession := map[bucket][]ActivityRecord{}
	for _, a := range acts {
		if !workingState(a.State) {
			continue
		}
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
