package pomodoro

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

func TestDayEventsRoundTripInOrder(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Unix(1_761_000_000, 0)
	for _, e := range []workday.Event{
		{At: t0, Kind: workday.Start, Value: "18:00"},
		{At: t0, Kind: workday.Break},
		{At: t0.Add(time.Hour), Kind: workday.Resume},
		{At: t0.Add(2 * time.Hour), Kind: workday.Stop},
	} {
		if err := s.RecordDayEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastDayEvents(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != workday.Break || got[2].Kind != workday.Stop {
		t.Fatalf("last 3 = %+v, want break, resume, stop ascending", got)
	}
	all, err := s.DayEventsBetween(t0, t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0].Kind != workday.Start || all[0].Value != "18:00" || !all[0].At.Equal(t0) {
		t.Fatalf("between = %+v", all)
	}
}

func TestDayEventsFromLastStartKeepAnOpenPeriodWhole(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Unix(1_761_000_000, 0)
	record := func(e workday.Event) {
		t.Helper()
		if err := s.RecordDayEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	record(workday.Event{At: t0, Kind: workday.Start, Value: "18:00"})
	record(workday.Event{At: t0.Add(time.Hour), Kind: workday.Stop})
	record(workday.Event{At: t0.Add(2 * time.Hour), Kind: workday.Start, Value: "20:00"})
	for i := range 300 {
		record(workday.Event{At: t0.Add(2*time.Hour + time.Duration(i)*time.Second), Kind: workday.StopAt, Value: "21:00"})
	}
	got, started, err := s.DayEventsFromLastStart()
	if err != nil || !started {
		t.Fatalf("started = %v %v", started, err)
	}
	if len(got) != 301 || got[0].Kind != workday.Start || got[0].Value != "20:00" {
		t.Fatalf("events = %d, first %+v", len(got), got[0])
	}
}

func TestDayEventsFromLastStartOnAStoreWithoutAStart(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordDayEvent(workday.Event{At: time.Unix(1_761_000_000, 0), Kind: workday.StopAt, Value: "19:00"}); err != nil {
		t.Fatal(err)
	}
	got, started, err := s.DayEventsFromLastStart()
	if err != nil || started || len(got) != 1 || got[0].Value != "19:00" {
		t.Fatalf("events = %+v started %v %v, want the stop_at and no start", got, started, err)
	}
}

func TestDayRepliesQueryAndPrune(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Unix(1_761_000_000, 0)
	for i, key := range []string{"a", "b", "a"} {
		r := workday.Reply{At: t0.Add(time.Duration(i) * time.Hour), Source: "mbp", Tool: "claude", SessionKey: key}
		if err := s.RecordDayReply(r); err != nil {
			t.Fatal(err)
		}
	}
	last, ok, err := s.LastDayReply()
	if err != nil || !ok || !last.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("last reply = %v %v %v", last, ok, err)
	}
	n, err := s.CountDayReplies(t0.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("count since +1h = %d %v, want 2", n, err)
	}
	rs, err := s.DayRepliesBetween(t0, t0.Add(90*time.Minute))
	if err != nil || len(rs) != 2 || rs[1].SessionKey != "b" || rs[1].Tool != "claude" {
		t.Fatalf("between = %+v %v", rs, err)
	}
	if err := s.RecordDayEvent(workday.Event{At: t0, Kind: workday.Start, Value: "18:00"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneDay(t0.Add(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountDayReplies(time.Unix(0, 0)); n != 2 {
		t.Errorf("replies after prune = %d, want 2", n)
	}
	if ev, _ := s.LastDayEvents(10); len(ev) != 0 {
		t.Errorf("events after prune = %+v, want none", ev)
	}
}

func TestLastDayReplyOnAnEmptyStore(t *testing.T) {
	s := openTestStore(t)
	if _, ok, err := s.LastDayReply(); err != nil || ok {
		t.Fatalf("LastDayReply = %v %v, want none", ok, err)
	}
}
