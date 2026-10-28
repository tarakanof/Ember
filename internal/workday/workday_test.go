package workday

import (
	"testing"
	"time"
)

var berlin = mustLoc("Europe/Berlin")

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

var cal = Calendar{DayStartHour: 4, Loc: berlin}

func at(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, berlin) }

func TestReplayBuildsPeriodsWithBreaks(t *testing.T) {
	log := Replay([]Event{
		{At: at(26, 10, 0), Kind: Start, Value: "18:00"},
		{At: at(26, 12, 0), Kind: Break},
		{At: at(26, 12, 30), Kind: Resume},
		{At: at(26, 17, 0), Kind: StopAt, Value: "19:00"},
		{At: at(26, 19, 0), Kind: Stop},
		{At: at(27, 9, 0), Kind: Start, Value: "00:00"},
		{At: at(27, 11, 0), Kind: Break},
	})
	if len(log.Periods) != 2 {
		t.Fatalf("periods = %d, want 2", len(log.Periods))
	}
	p := log.Periods[0]
	if p.StopAt != "19:00" || !p.End.Equal(at(26, 19, 0)) || p.Auto {
		t.Fatalf("first period = %+v", p)
	}
	if got := p.OnSec(at(28, 0, 0)); got != 8*3600+30*60 {
		t.Errorf("on_sec = %d, want 8.5 h", got)
	}
	if got := p.BreakSec(at(28, 0, 0)); got != 30*60 {
		t.Errorf("break_sec = %d, want 30 min", got)
	}
	open := log.Periods[1]
	if !open.Open() || !open.OnBreak() {
		t.Fatalf("second period open=%v on_break=%v, want both", open.Open(), open.OnBreak())
	}
	now := at(27, 11, 20)
	if got := open.BreakSec(now); got != 20*60 {
		t.Errorf("open break_sec = %d, want 20 min", got)
	}
	if got := open.CurrentBreakSec(now); got != 20*60 {
		t.Errorf("current break = %d, want 20 min", got)
	}
	if got := open.OnSec(now); got != 2*3600 {
		t.Errorf("open on_sec = %d, want 2 h", got)
	}
	if log.LastStopAt != "00:00" {
		t.Errorf("last stop_at = %q, want 00:00", log.LastStopAt)
	}
}

func TestReplayIgnoresEventsOutsideAPeriod(t *testing.T) {
	log := Replay([]Event{
		{At: at(26, 9, 0), Kind: Resume},
		{At: at(26, 9, 1), Kind: Break},
		{At: at(26, 9, 2), Kind: Stop},
		{At: at(26, 9, 3), Kind: StopAt, Value: "20:00"},
		{At: at(26, 10, 0), Kind: Start, Value: "18:00"},
		{At: at(26, 10, 1), Kind: Start, Value: "17:00"},
		{At: at(26, 11, 0), Kind: AutoStop},
	})
	if len(log.Periods) != 1 {
		t.Fatalf("periods = %+v, want one", log.Periods)
	}
	if p := log.Periods[0]; p.StopAt != "18:00" || !p.Auto {
		t.Errorf("period = %+v, want stop_at 18:00 auto", p)
	}
	if log.LastStopAt != "18:00" {
		t.Errorf("last stop_at = %q", log.LastStopAt)
	}
}

func TestStopInstantStaysInTheStartsLogicalDay(t *testing.T) {
	cases := []struct {
		name  string
		start time.Time
		hhmm  string
		want  time.Time
	}{
		{"evening", at(27, 11, 18), "18:00", at(27, 18, 0)},
		{"midnight", at(27, 11, 18), "00:00", at(28, 0, 0)},
		{"after midnight", at(27, 11, 18), "01:30", at(28, 1, 30)},
		{"day start is the end", at(27, 11, 18), "04:00", at(28, 4, 0)},
		{"start after midnight", at(28, 1, 0), "00:00", at(28, 0, 0)},
		{"target before the start", at(27, 11, 18), "10:00", at(27, 10, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := cal.StopInstant(c.start, c.hhmm)
			if !ok || !got.Equal(c.want) {
				t.Fatalf("StopInstant(%v, %s) = %v %v, want %v", c.start, c.hhmm, got, ok, c.want)
			}
		})
	}
}

func TestResolveAtPicksTheLatestPastWallTime(t *testing.T) {
	now := at(28, 0, 20)
	if got, ok := cal.ResolveAt("23:50", now); !ok || !got.Equal(at(27, 23, 50)) {
		t.Errorf("23:50 = %v %v", got, ok)
	}
	if got, ok := cal.ResolveAt("00:20", now); !ok || !got.Equal(now) {
		t.Errorf("00:20 = %v %v", got, ok)
	}
	if got, ok := cal.ResolveAt("00:25", now); !ok || !got.Equal(at(27, 0, 25)) {
		t.Errorf("00:25 = %v %v", got, ok)
	}
	if _, ok := cal.ResolveAt("25:00", now); ok {
		t.Error("25:00 resolved")
	}
}

func TestDayKeyUsesTheDayStartHour(t *testing.T) {
	if got := cal.DayKey(at(28, 3, 59)); got != "2026-10-27" {
		t.Errorf("03:59 = %s", got)
	}
	if got := cal.DayKey(at(28, 4, 0)); got != "2026-10-28" {
		t.Errorf("04:00 = %s", got)
	}
}

func seededWeek() ([]Event, []Reply) {
	ev := []Event{
		{At: at(19, 10, 0), Kind: Start, Value: "18:00"},
		{At: at(19, 18, 10), Kind: Stop},
		{At: at(20, 11, 0), Kind: Start, Value: "18:00"},
		{At: at(20, 13, 0), Kind: Break},
		{At: at(20, 14, 0), Kind: Resume},
		{At: at(20, 18, 30), Kind: Stop},
		{At: at(21, 9, 0), Kind: Start, Value: "00:00"},
		{At: at(22, 0, 40), Kind: AutoStop},
		{At: at(22, 10, 0), Kind: Start, Value: "23:30"},
		{At: at(22, 23, 0), Kind: Stop},
		{At: at(25, 12, 0), Kind: Start, Value: "20:00"},
	}
	r := func(day, hh, mm int, key string) Reply {
		return Reply{At: at(day, hh, mm), Source: "mbp", Tool: "claude", SessionKey: key}
	}
	replies := []Reply{
		r(19, 9, 30, "a"),
		r(19, 12, 0, "a"),
		r(19, 22, 0, "b"),
		r(20, 2, 0, "b"),
		r(20, 12, 0, "a"),
		r(21, 23, 50, "a"),
		r(22, 0, 40, "a"),
		r(22, 1, 0, "a"),
		r(23, 15, 0, "c"),
		r(25, 13, 0, "a"),
	}
	return ev, replies
}

func TestDaysForASeededWeek(t *testing.T) {
	ev, replies := seededWeek()
	now := at(25, 15, 0)
	days := cal.Days(Replay(ev), replies, now, 7, 15*time.Minute)
	if len(days) != 7 {
		t.Fatalf("days = %d, want 7", len(days))
	}
	byDate := map[string]Day{}
	for _, d := range days {
		byDate[d.Date] = d
	}
	if days[0].Date != "2026-10-25" || days[6].Date != "2026-10-19" {
		t.Fatalf("order = %s … %s, want newest first", days[0].Date, days[6].Date)
	}

	d := byDate["2026-10-19"]
	if !d.InRange || d.OnSec != 8*3600+10*60 || d.RepliesOn != 1 || d.RepliesOff != 3 || d.Auto {
		t.Errorf("19th = %+v, want in range (18:10 ≤ 18:15), 8h10, 1 on, 3 off", d)
	}
	if !d.FirstReply.Equal(at(19, 9, 30)) || !d.LastReply.Equal(at(20, 2, 0)) {
		t.Errorf("19th replies %v … %v", d.FirstReply, d.LastReply)
	}

	d = byDate["2026-10-20"]
	if d.InRange || d.OnSec != 6*3600+30*60 || d.BreakSec != 3600 || d.RepliesOn != 1 || d.RepliesOff != 0 {
		t.Errorf("20th = %+v, want out of range (18:30 > 18:15), 6h30 on, 1h break", d)
	}
	if len(d.Breaks) != 1 || !d.Breaks[0].Start.Equal(at(20, 13, 0)) {
		t.Errorf("20th breaks = %+v", d.Breaks)
	}

	d = byDate["2026-10-21"]
	if !d.Auto || d.InRange || d.StopTarget != "00:00" || d.RepliesOn != 2 || d.RepliesOff != 1 {
		t.Errorf("21st = %+v, want auto, out of range (00:40 > 00:15), 2 on, 1 off", d)
	}

	d = byDate["2026-10-22"]
	if !d.InRange || d.RepliesOn != 0 || d.RepliesOff != 0 {
		t.Errorf("22nd = %+v, want in range", d)
	}

	d = byDate["2026-10-23"]
	if !d.Start.IsZero() || d.OnSec != 0 || d.RepliesOff != 1 || d.InRange {
		t.Errorf("23rd = %+v, want a day off with one off-clock reply", d)
	}

	d = byDate["2026-10-25"]
	if !d.Stop.IsZero() || d.OnSec != 3*3600 || d.InRange || d.RepliesOn != 1 {
		t.Errorf("25th = %+v, want open, 3 h so far", d)
	}
}

func TestWeeksRollUpDays(t *testing.T) {
	ev, replies := seededWeek()
	ev = append(ev, Event{At: at(25, 15, 0), Kind: Stop})
	now := at(27, 15, 0)
	days := cal.Days(Replay(ev), replies, now, 9, 15*time.Minute)
	weeks := Weeks(days)
	if len(weeks) != 2 {
		t.Fatalf("weeks = %+v", weeks)
	}
	w := weeks[1]
	if w.Week != "2026-W43" || w.DaysWorked != 5 || w.DaysInRange != 3 || w.RepliesOff != 5 {
		t.Errorf("W43 = %+v, want 5 worked, 3 in range, 5 off", w)
	}
	want := (8*3600 + 10*60) + (6*3600 + 30*60) + (15*3600 + 40*60) + 13*3600 + 3*3600
	if w.OnSec != want {
		t.Errorf("W43 on_sec = %d, want %d", w.OnSec, want)
	}
	if weeks[0].Week != "2026-W44" || weeks[0].DaysWorked != 0 {
		t.Errorf("W44 = %+v", weeks[0])
	}
}
