package workday

import (
	"fmt"
	"slices"
	"time"
)

type Kind string

const (
	Start    Kind = "start"
	Break    Kind = "break"
	Resume   Kind = "resume"
	Stop     Kind = "stop"
	AutoStop Kind = "auto_stop"
	StopAt   Kind = "stop_at"
)

type Event struct {
	At    time.Time
	Kind  Kind
	Value string
}

type Reply struct {
	At         time.Time
	Source     string
	Tool       string
	SessionKey string
}

type Interval struct {
	Start time.Time
	End   time.Time
}

type Period struct {
	Start  time.Time
	End    time.Time
	StopAt string
	Breaks []Interval
	Auto   bool
}

func (p Period) Open() bool { return p.End.IsZero() }

func (p Period) OnBreak() bool {
	n := len(p.Breaks)
	return p.Open() && n > 0 && p.Breaks[n-1].End.IsZero()
}

func (p Period) endOr(now time.Time) time.Time {
	if p.Open() {
		return now
	}
	return p.End
}

func (p Period) BreakSec(now time.Time) int {
	end := p.endOr(now)
	total := time.Duration(0)
	for _, b := range p.Breaks {
		e := b.End
		if e.IsZero() {
			e = end
		}
		if e.After(b.Start) {
			total += e.Sub(b.Start)
		}
	}
	return int(total / time.Second)
}

func (p Period) CurrentBreakSec(now time.Time) int {
	if !p.OnBreak() {
		return 0
	}
	return int(now.Sub(p.Breaks[len(p.Breaks)-1].Start) / time.Second)
}

func (p Period) OnSec(now time.Time) int {
	span := int(p.endOr(now).Sub(p.Start) / time.Second)
	return max(0, span-p.BreakSec(now))
}

func (p Period) WorkingSince() time.Time {
	if n := len(p.Breaks); n > 0 && !p.Breaks[n-1].End.IsZero() {
		return p.Breaks[n-1].End
	}
	return p.Start
}

func (p Period) covers(t time.Time) bool {
	return !t.Before(p.Start) && (p.Open() || !t.After(p.End))
}

type Log struct {
	Periods    []Period
	LastStopAt string
}

func (l Log) Current() *Period {
	if n := len(l.Periods); n > 0 && l.Periods[n-1].Open() {
		return &l.Periods[n-1]
	}
	return nil
}

func (l Log) LastClockOut() time.Time {
	for i := len(l.Periods) - 1; i >= 0; i-- {
		if !l.Periods[i].Open() {
			return l.Periods[i].End
		}
	}
	return time.Time{}
}

func Replay(events []Event) Log {
	var log Log
	for _, e := range events {
		cur := log.Current()
		switch e.Kind {
		case Start:
			if cur == nil {
				log.Periods = append(log.Periods, Period{Start: e.At, StopAt: e.Value})
				log.LastStopAt = e.Value
			}
		case StopAt:
			if cur != nil {
				cur.StopAt = e.Value
			}
			log.LastStopAt = e.Value
		case Break:
			if cur != nil && !cur.OnBreak() {
				cur.Breaks = append(cur.Breaks, Interval{Start: e.At})
			}
		case Resume:
			if cur != nil && cur.OnBreak() {
				cur.Breaks[len(cur.Breaks)-1].End = e.At
			}
		case Stop, AutoStop:
			if cur != nil {
				cur.Close(e.At, e.Kind == AutoStop)
			}
		}
	}
	return log
}

func (p *Period) Close(at time.Time, auto bool) {
	if p.OnBreak() {
		p.Breaks[len(p.Breaks)-1].End = at
	}
	p.End, p.Auto = at, auto
}

func ParseHHMM(s string) (h, m int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

type Calendar struct {
	DayStartHour int
	Loc          *time.Location
}

func (c Calendar) DayStart(t time.Time) time.Time {
	d := t.In(c.Loc).Add(-time.Duration(c.DayStartHour) * time.Hour)
	return time.Date(d.Year(), d.Month(), d.Day(), c.DayStartHour, 0, 0, 0, c.Loc)
}

func (c Calendar) DayKey(t time.Time) string {
	return t.In(c.Loc).Add(-time.Duration(c.DayStartHour) * time.Hour).Format("2006-01-02")
}

func (c Calendar) StopInstant(start time.Time, hhmm string) (time.Time, bool) {
	h, m, ok := ParseHHMM(hhmm)
	if !ok {
		return time.Time{}, false
	}
	ds := c.DayStart(start)
	at := time.Date(ds.Year(), ds.Month(), ds.Day(), h, m, 0, 0, c.Loc)
	if !at.After(ds) {
		at = time.Date(ds.Year(), ds.Month(), ds.Day()+1, h, m, 0, 0, c.Loc)
	}
	return at, true
}

func (c Calendar) ResolveAt(hhmm string, now time.Time) (time.Time, bool) {
	h, m, ok := ParseHHMM(hhmm)
	if !ok {
		return time.Time{}, false
	}
	n := now.In(c.Loc)
	at := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, c.Loc)
	if at.After(now) {
		at = time.Date(n.Year(), n.Month(), n.Day()-1, h, m, 0, 0, c.Loc)
	}
	return at, true
}

func (c Calendar) InRange(p Period, grace time.Duration) bool {
	if p.Open() {
		return false
	}
	stop, ok := c.StopInstant(p.Start, p.StopAt)
	return ok && !p.End.After(stop.Add(grace))
}

type Day struct {
	Date       string
	Start      time.Time
	Stop       time.Time
	StopTarget string
	OnSec      int
	BreakSec   int
	Auto       bool
	InRange    bool
	RepliesOn  int
	RepliesOff int
	FirstReply time.Time
	LastReply  time.Time
	Breaks     []Interval
}

func (d Day) Worked() bool { return !d.Start.IsZero() }

func (c Calendar) Days(log Log, replies []Reply, now time.Time, n int, grace time.Duration) []Day {
	today := c.DayStart(now)
	out := make([]Day, n)
	index := make(map[string]int, n)
	for i := range n {
		ds := time.Date(today.Year(), today.Month(), today.Day()-i, c.DayStartHour, 0, 0, 0, c.Loc)
		key := ds.Format("2006-01-02")
		out[i] = Day{Date: key}
		index[key] = i
	}
	for _, p := range log.Periods {
		i, ok := index[c.DayKey(p.Start)]
		if !ok {
			continue
		}
		d := &out[i]
		if d.Start.IsZero() {
			d.Start = p.Start
		}
		d.Stop, d.StopTarget, d.Auto = p.End, p.StopAt, p.Auto
		d.OnSec += p.OnSec(now)
		d.BreakSec += p.BreakSec(now)
		d.InRange = c.InRange(p, grace)
		d.Breaks = append(d.Breaks, p.Breaks...)
	}
	for _, r := range replies {
		i, ok := index[c.DayKey(r.At)]
		if !ok {
			continue
		}
		d := &out[i]
		if slices.ContainsFunc(log.Periods, func(p Period) bool { return p.covers(r.At) }) {
			d.RepliesOn++
		} else {
			d.RepliesOff++
		}
		if d.FirstReply.IsZero() || r.At.Before(d.FirstReply) {
			d.FirstReply = r.At
		}
		if r.At.After(d.LastReply) {
			d.LastReply = r.At
		}
	}
	return out
}

type Week struct {
	Week        string
	DaysWorked  int
	DaysInRange int
	OnSec       int
	RepliesOff  int
}

func Weeks(days []Day) []Week {
	var out []Week
	index := map[string]int{}
	for _, d := range days {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		key := fmt.Sprintf("%04d-W%02d", y, w)
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, Week{Week: key})
		}
		wk := &out[i]
		if d.Worked() {
			wk.DaysWorked++
		}
		if d.InRange {
			wk.DaysInRange++
		}
		wk.OnSec += d.OnSec
		wk.RepliesOff += d.RepliesOff
	}
	return out
}

func (c Calendar) WeekStart(now time.Time) time.Time {
	ds := c.DayStart(now)
	back := (int(ds.Weekday()) + 6) % 7
	return time.Date(ds.Year(), ds.Month(), ds.Day()-back, c.DayStartHour, 0, 0, 0, c.Loc)
}

func (c Calendar) DayEnd(t time.Time) time.Time {
	ds := c.DayStart(t)
	return time.Date(ds.Year(), ds.Month(), ds.Day()+1, c.DayStartHour, 0, 0, 0, c.Loc)
}

func (c Calendar) DaysBetween(from, to time.Time) int {
	date := func(t time.Time) time.Time {
		ds := c.DayStart(t)
		return time.Date(ds.Year(), ds.Month(), ds.Day(), 0, 0, 0, 0, time.UTC)
	}
	return int(date(to).Sub(date(from)) / (24 * time.Hour))
}
