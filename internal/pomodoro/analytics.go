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

// DayWork sessionizes the focus phases that started on the calendar day of
// `day` (in loc) and summarises them. now lets WorkEnd reflect an in-progress
// day (it is clamped to the last activity, never earlier than the last block).
func DayWork(recs []PhaseRecord, day time.Time, gap time.Duration, loc *time.Location) DaySummary {
	dayKey := day.In(loc).Format("2006-01-02")
	var inDay []PhaseRecord
	for _, r := range recs {
		if r.Phase == PhaseFocus && r.StartedAt.In(loc).Format("2006-01-02") == dayKey {
			inDay = append(inDay, r)
		}
	}
	sessions := WorkSessions(inDay, gap)
	d := DaySummary{Date: dayKey, Sessions: len(sessions)}
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

// ActiveFocusDays returns the set of calendar days (YYYY-MM-DD in loc) that have
// at least one completed focus phase.
func ActiveFocusDays(recs []PhaseRecord, loc *time.Location) map[string]bool {
	m := make(map[string]bool)
	for _, r := range recs {
		if r.Phase == PhaseFocus && r.Completed {
			m[r.EndedAt.In(loc).Format("2006-01-02")] = true
		}
	}
	return m
}

// Streaks computes the current and longest streak from a set of active days
// (keyed YYYY-MM-DD). The current streak counts qualifying days ending at
// `today`, tolerating up to graceDays missed days within the look-back window
// before it ends (graceDays=0 is the strict "miss-resets" rule). Missed days do
// not themselves add to the count — only qualifying days do.
func Streaks(active map[string]bool, today time.Time, graceDays int) StreakInfo {
	loc := today.Location()
	d := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)

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
// at the requested granularity. Abandoned and non-focus phases are excluded.
func Rollup(recs []PhaseRecord, gran Granularity, loc *time.Location) []Bucket {
	idx := make(map[string]*Bucket)
	var order []string
	for _, r := range recs {
		if r.Phase != PhaseFocus || !r.Completed {
			continue
		}
		key := bucketKey(r.EndedAt.In(loc), gran)
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
