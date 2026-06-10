package pomodoro

import (
	"fmt"
	"sort"
	"time"
)

// This file is the analytics layer: pure functions over decoded phase rows plus
// one Store fetch. It is deliberately separate from store.go/engine.go so the
// richer stats can grow without touching the timer's hot path. All computation
// is pure (no I/O, clock injected via the caller's `now`/`loc`), so the views
// are deterministic and trivially testable.

// PhaseRecord is one persisted phase row decoded for analysis.
type PhaseRecord struct {
	StartedAt  time.Time
	EndedAt    time.Time
	Phase      Phase
	PlannedSec int
	ActualSec  int
	Completed  bool
	Reason     string
}

// PhasesBetween returns the phase rows whose ended_at falls in [lo, hi), oldest
// first. Times are reconstructed in the local location (Unix instants are
// timezone-agnostic; callers pass a *time.Location to the pure functions for
// day/hour bucketing).
func (s *Store) PhasesBetween(lo, hi time.Time) ([]PhaseRecord, error) {
	rows, err := s.db.Query(
		`SELECT started_at, ended_at, phase, planned_sec, actual_sec, completed, reason
		   FROM phases
		  WHERE ended_at >= ? AND ended_at < ?
		  ORDER BY ended_at ASC`,
		lo.Unix(), hi.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("phases between: %w", err)
	}
	defer rows.Close()

	var out []PhaseRecord
	for rows.Next() {
		var (
			started, ended int64
			phase, reason  string
			planned, act   int
			completed      int
		)
		if err := rows.Scan(&started, &ended, &phase, &planned, &act, &completed, &reason); err != nil {
			return nil, fmt.Errorf("scan phase: %w", err)
		}
		out = append(out, PhaseRecord{
			StartedAt:  time.Unix(started, 0),
			EndedAt:    time.Unix(ended, 0),
			Phase:      Phase(phase),
			PlannedSec: planned,
			ActualSec:  act,
			Completed:  completed != 0,
			Reason:     reason,
		})
	}
	return out, rows.Err()
}

// ActivityRecord is one persisted AI-coding-session activity heartbeat —
// evidence that a session was actively working at recorded_at.
type ActivityRecord struct {
	At         time.Time
	Source     string
	Tool       string
	SessionKey string
	State      string
}

// RecordActivity inserts one activity heartbeat.
func (s *Store) RecordActivity(at time.Time, source, tool, sessionKey, state string) error {
	_, err := s.db.Exec(
		`INSERT INTO activity (recorded_at, source, tool, session_key, state)
		 VALUES (?, ?, ?, ?, ?)`,
		at.Unix(), source, tool, sessionKey, state,
	)
	if err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	return nil
}

// ActivityBetween returns activity heartbeats in [lo, hi), oldest first.
func (s *Store) ActivityBetween(lo, hi time.Time) ([]ActivityRecord, error) {
	rows, err := s.db.Query(
		`SELECT recorded_at, source, tool, session_key, state
		   FROM activity
		  WHERE recorded_at >= ? AND recorded_at < ?
		  ORDER BY recorded_at ASC`,
		lo.Unix(), hi.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("activity between: %w", err)
	}
	defer rows.Close()

	var out []ActivityRecord
	for rows.Next() {
		var (
			at                              int64
			source, tool, sessionKey, state string
		)
		if err := rows.Scan(&at, &source, &tool, &sessionKey, &state); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		out = append(out, ActivityRecord{
			At: time.Unix(at, 0), Source: source, Tool: tool, SessionKey: sessionKey, State: state,
		})
	}
	return out, rows.Err()
}

// CompletionStat summarises focus-phase outcomes over a record set. Abandoned =
// any non-completed focus phase (stopped / skipped / max_session).
type CompletionStat struct {
	CompletedFocus int     `json:"completed_focus"`
	AbandonedFocus int     `json:"abandoned_focus"`
	TotalFocus     int     `json:"total_focus"`
	CompletionRate float64 `json:"completion_rate"` // 0..1; 0 when no focus phases
	FocusSec       int     `json:"focus_sec"`       // actual seconds across completed focus
}

// CompletionStats computes the focus completion summary. Non-focus phases are
// ignored.
func CompletionStats(recs []PhaseRecord) CompletionStat {
	var c CompletionStat
	for _, r := range recs {
		if r.Phase != PhaseFocus {
			continue
		}
		c.TotalFocus++
		if r.Completed {
			c.CompletedFocus++
			c.FocusSec += r.ActualSec
		} else {
			c.AbandonedFocus++
		}
	}
	if c.TotalFocus > 0 {
		c.CompletionRate = float64(c.CompletedFocus) / float64(c.TotalFocus)
	}
	return c
}

// WorkSession is a run of focus blocks with no internal gap longer than the
// sessionization threshold. It models "a stretch of work."
type WorkSession struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	ActiveSec int       `json:"active_sec"` // summed focus time inside the session
	Blocks    int       `json:"blocks"`     // number of focus phases merged
}

// SpanSec is wall-clock length start→end (active + bridged breaks).
func (w WorkSession) SpanSec() int { return int(w.End.Sub(w.Start) / time.Second) }

// BreakSec is the bridged idle time inside the session (span − active).
func (w WorkSession) BreakSec() int {
	if b := w.SpanSec() - w.ActiveSec; b > 0 {
		return b
	}
	return 0
}

// WorkSessions groups focus phases into work sessions, bridging any gap ≤ gap
// into the current session and starting a new one otherwise. Input order does
// not matter. Only focus phases participate (breaks are the gaps).
func WorkSessions(recs []PhaseRecord, gap time.Duration) []WorkSession {
	type iv struct{ s, e time.Time }
	var ivs []iv
	for _, r := range recs {
		if r.Phase == PhaseFocus && r.EndedAt.After(r.StartedAt) {
			ivs = append(ivs, iv{r.StartedAt, r.EndedAt})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].s.Before(ivs[j].s) })

	var out []WorkSession
	for _, v := range ivs {
		dur := int(v.e.Sub(v.s) / time.Second)
		if n := len(out); n > 0 && v.s.Sub(out[n-1].End) <= gap {
			cur := &out[n-1]
			if v.e.After(cur.End) {
				cur.End = v.e
			}
			cur.ActiveSec += dur
			cur.Blocks++
			continue
		}
		out = append(out, WorkSession{Start: v.s, End: v.e, ActiveSec: dur, Blocks: 1})
	}
	return out
}

// Interval is a time span [Start, End].
type Interval struct {
	Start time.Time
	End   time.Time
}

// mergeIntervals returns the union of the given intervals, additionally bridging
// any gap ≤ bridge into one interval. Input order is irrelevant. Overlaps are
// de-duplicated (the union never double-counts), so totalSec of the result is
// true wall-clock coverage. Zero-width intervals are kept (they can anchor a
// session) but add no duration.
func mergeIntervals(ivs []Interval, bridge time.Duration) []Interval {
	if len(ivs) == 0 {
		return nil
	}
	sorted := append([]Interval(nil), ivs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	out := []Interval{sorted[0]}
	for _, v := range sorted[1:] {
		cur := &out[len(out)-1]
		if !v.Start.After(cur.End.Add(bridge)) { // v.Start <= cur.End + bridge → merge
			if v.End.After(cur.End) {
				cur.End = v.End
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

// totalSec sums interval durations (non-negative).
func totalSec(ivs []Interval) int {
	s := 0
	for _, v := range ivs {
		if d := int(v.End.Sub(v.Start) / time.Second); d > 0 {
			s += d
		}
	}
	return s
}

// activitySpans reconstructs continuous active spans from discrete activity
// heartbeats: consecutive heartbeats no more than maxGap apart form one span
// [first, last]. An isolated heartbeat yields a zero-width span.
func activitySpans(acts []ActivityRecord, maxGap time.Duration) []Interval {
	if len(acts) == 0 {
		return nil
	}
	ivs := make([]Interval, 0, len(acts))
	for _, a := range acts {
		ivs = append(ivs, Interval{a.At, a.At})
	}
	return mergeIntervals(ivs, maxGap)
}

// DaySummary is the headline work-hours rollup for one calendar day.
type DaySummary struct {
	Date       string    `json:"date"`
	WorkStart  time.Time `json:"work_start"`
	WorkEnd    time.Time `json:"work_end"`
	SpanSec    int       `json:"span_sec"`    // work_end − work_start
	ActiveSec  int       `json:"active_sec"`  // summed focus across sessions
	BreakSec   int       `json:"break_sec"`   // span − active
	Sessions   int       `json:"sessions"`    // number of work sessions
	LongestSec int       `json:"longest_sec"` // longest single session's active time
}

// dayKey is the logical calendar day a timestamp belongs to, accounting for a
// configurable day-start hour: with dayStartHour=4, anything before 04:00 local
// counts toward the previous day (so a 01:00 night-owl session is still
// "yesterday's work"). dayStartHour=0 is naive calendar midnight.
func dayKey(t time.Time, dayStartHour int, loc *time.Location) string {
	return t.In(loc).Add(-time.Duration(dayStartHour) * time.Hour).Format("2006-01-02")
}

// DayWork sessionizes the focus phases on the logical day of `day` (per
// dayStartHour, in loc) and summarises them. WorkEnd reflects the last activity;
// pass `now` as `day` for an in-progress day to anchor the window to the present.
func DayWork(recs []PhaseRecord, day time.Time, gap time.Duration, dayStartHour int, loc *time.Location) DaySummary {
	key := dayKey(day, dayStartHour, loc)
	var inDay []PhaseRecord
	for _, r := range recs {
		if r.Phase == PhaseFocus && dayKey(r.StartedAt, dayStartHour, loc) == key {
			inDay = append(inDay, r)
		}
	}
	sessions := WorkSessions(inDay, gap)
	d := DaySummary{Date: key, Sessions: len(sessions)}
	if len(sessions) == 0 {
		return d
	}
	d.WorkStart = sessions[0].Start
	d.WorkEnd = sessions[len(sessions)-1].End
	for _, s := range sessions {
		d.ActiveSec += s.ActiveSec
		if s.ActiveSec > d.LongestSec {
			d.LongestSec = s.ActiveSec
		}
	}
	d.SpanSec = int(d.WorkEnd.Sub(d.WorkStart) / time.Second)
	d.BreakSec = d.SpanSec - d.ActiveSec
	if d.BreakSec < 0 {
		d.BreakSec = 0
	}
	return d
}

// DayWorkOverlay is DayWork extended with AI-coding-session activity: focus
// blocks and reconstructed activity spans (heartbeats merged within activityGap)
// are unioned — so overlap is never double-counted — then sessionized with gap.
// ActiveSec is true active wall-clock (focus ∪ activity); span/break/longest are
// derived as in DayWork.
func DayWorkOverlay(focus []PhaseRecord, acts []ActivityRecord, day time.Time, gap, activityGap time.Duration, dayStartHour int, loc *time.Location) DaySummary {
	key := dayKey(day, dayStartHour, loc)

	var ivs []Interval
	for _, r := range focus {
		if r.Phase == PhaseFocus && r.EndedAt.After(r.StartedAt) && dayKey(r.StartedAt, dayStartHour, loc) == key {
			ivs = append(ivs, Interval{r.StartedAt, r.EndedAt})
		}
	}
	var dayActs []ActivityRecord
	for _, a := range acts {
		if dayKey(a.At, dayStartHour, loc) == key {
			dayActs = append(dayActs, a)
		}
	}
	ivs = append(ivs, activitySpans(dayActs, activityGap)...)

	active := mergeIntervals(ivs, 0) // true union → real active time, no double count
	d := DaySummary{Date: key}
	if len(active) == 0 {
		return d
	}
	sessions := mergeIntervals(active, gap) // bridge short idle gaps into work sessions
	d.Sessions = len(sessions)
	d.WorkStart = sessions[0].Start
	d.WorkEnd = sessions[len(sessions)-1].End
	d.SpanSec = int(d.WorkEnd.Sub(d.WorkStart) / time.Second)
	d.ActiveSec = totalSec(active)

	// LongestSec: the most active wall-clock within a single session.
	for _, s := range sessions {
		secs := 0
		for _, iv := range active {
			if !iv.Start.Before(s.Start) && !iv.End.After(s.End) {
				secs += int(iv.End.Sub(iv.Start) / time.Second)
			}
		}
		if secs > d.LongestSec {
			d.LongestSec = secs
		}
	}
	d.BreakSec = d.SpanSec - d.ActiveSec
	if d.BreakSec < 0 {
		d.BreakSec = 0
	}
	return d
}

// WeekdayHourHeatmap returns completed-focus minutes bucketed by [weekday][hour]
// (weekday 0=Sunday..6=Saturday), attributed to the hour the phase started in
// loc. Powers the "when am I most productive" grid heatmap.
func WeekdayHourHeatmap(recs []PhaseRecord, loc *time.Location) [7][24]int {
	var h [7][24]int
	for _, r := range recs {
		if r.Phase != PhaseFocus || !r.Completed {
			continue
		}
		t := r.StartedAt.In(loc)
		h[int(t.Weekday())][t.Hour()] += r.ActualSec / 60
	}
	return h
}

// StreakInfo is the current and best run of qualifying days.
type StreakInfo struct {
	Current int `json:"current"`
	Longest int `json:"longest"`
}

// ActiveFocusDays returns the set of logical days (YYYY-MM-DD, per dayStartHour
// in loc) that have at least one completed focus phase.
func ActiveFocusDays(recs []PhaseRecord, dayStartHour int, loc *time.Location) map[string]bool {
	m := make(map[string]bool)
	for _, r := range recs {
		if r.Phase == PhaseFocus && r.Completed {
			m[dayKey(r.EndedAt, dayStartHour, loc)] = true
		}
	}
	return m
}

// Streaks computes the current and longest streak from a set of active days
// (keyed YYYY-MM-DD, as produced by ActiveFocusDays with the same dayStartHour).
// The current streak counts qualifying days ending at the logical day of
// `today`, tolerating up to graceDays missed days within the look-back window
// before it ends (graceDays=0 is the strict "miss-resets" rule). Missed days do
// not themselves add to the count — only qualifying days do.
func Streaks(active map[string]bool, today time.Time, dayStartHour, graceDays int) StreakInfo {
	loc := today.Location()
	d, _ := time.ParseInLocation("2006-01-02", dayKey(today, dayStartHour, loc), loc)

	current, misses := 0, 0
	for i := 0; i < 4000; i++ { // hard bound: ~11y look-back
		if active[d.Format("2006-01-02")] {
			current++
		} else {
			misses++
			if misses > graceDays {
				break
			}
		}
		d = d.AddDate(0, 0, -1)
	}
	return StreakInfo{Current: current, Longest: longestRun(active)}
}

// longestRun is the longest run of consecutive calendar days in the set.
func longestRun(active map[string]bool) int {
	if len(active) == 0 {
		return 0
	}
	days := make([]time.Time, 0, len(active))
	for k := range active {
		if t, err := time.Parse("2006-01-02", k); err == nil {
			days = append(days, t)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	best, run := 1, 1
	for i := 1; i < len(days); i++ {
		if days[i].Equal(days[i-1].AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > best {
			best = run
		}
	}
	return best
}

// Granularity selects the bucket size for Rollup.
type Granularity int

const (
	GranDay Granularity = iota
	GranWeek
	GranMonth
)

// Bucket is one time bucket of completed-focus activity.
type Bucket struct {
	Key      string `json:"key"` // "2006-01-02" | "2006-W%02d" (ISO) | "2006-01"
	FocusMin int    `json:"focus_min"`
	Sessions int    `json:"sessions"`
}

// Rollup aggregates completed focus phases into chronologically-ordered buckets
// at the requested granularity, honouring dayStartHour for the day boundary.
// Abandoned and non-focus phases are excluded.
func Rollup(recs []PhaseRecord, gran Granularity, dayStartHour int, loc *time.Location) []Bucket {
	idx := make(map[string]*Bucket)
	var order []string
	for _, r := range recs {
		if r.Phase != PhaseFocus || !r.Completed {
			continue
		}
		shifted := r.EndedAt.In(loc).Add(-time.Duration(dayStartHour) * time.Hour)
		key := bucketKey(shifted, gran)
		b := idx[key]
		if b == nil {
			b = &Bucket{Key: key}
			idx[key] = b
			order = append(order, key)
		}
		b.Sessions++
		b.FocusMin += r.ActualSec / 60
	}
	sort.Strings(order) // keys are lexicographically chronological for all grains
	out := make([]Bucket, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out
}

func bucketKey(t time.Time, gran Granularity) string {
	switch gran {
	case GranWeek:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case GranMonth:
		return t.Format("2006-01")
	default:
		return t.Format("2006-01-02")
	}
}
