package pomodoro

import (
	"testing"
	"time"
)

// utc builds a UTC time for terse test fixtures.
func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

// focus builds a completed/abandoned focus PhaseRecord spanning [start, start+dur).
func focus(start time.Time, durMin int, completed bool, reason string) PhaseRecord {
	return PhaseRecord{
		StartedAt:  start,
		EndedAt:    start.Add(time.Duration(durMin) * time.Minute),
		Phase:      PhaseFocus,
		PlannedSec: durMin * 60,
		ActualSec:  durMin * 60,
		Completed:  completed,
		Reason:     reason,
	}
}

func TestCompletionStats(t *testing.T) {
	recs := []PhaseRecord{
		focus(utc(2026, 6, 10, 9, 0), 25, true, "completed"),
		focus(utc(2026, 6, 10, 9, 30), 25, true, "completed"),
		focus(utc(2026, 6, 10, 10, 0), 10, false, "stopped"),
		focus(utc(2026, 6, 10, 10, 30), 5, false, "skipped"),
		// a break phase must be ignored entirely
		{StartedAt: utc(2026, 6, 10, 11, 0), EndedAt: utc(2026, 6, 10, 11, 5),
			Phase: PhaseShort, PlannedSec: 300, ActualSec: 300, Completed: true, Reason: "completed"},
	}
	got := CompletionStats(recs)
	if got.TotalFocus != 4 || got.CompletedFocus != 2 || got.AbandonedFocus != 2 {
		t.Fatalf("counts: %+v", got)
	}
	if got.CompletionRate != 0.5 {
		t.Errorf("rate = %v, want 0.5", got.CompletionRate)
	}
	if got.FocusSec != 2*25*60 {
		t.Errorf("focus sec = %d, want %d", got.FocusSec, 2*25*60)
	}
}

func TestCompletionStatsEmpty(t *testing.T) {
	got := CompletionStats(nil)
	if got.TotalFocus != 0 || got.CompletionRate != 0 {
		t.Fatalf("empty should be zero-valued, got %+v", got)
	}
}

func TestWorkSessionsBridgesSmallGaps(t *testing.T) {
	// Two 25-min focus blocks 5 min apart → one session; then a 40-min gap → a
	// second session.
	recs := []PhaseRecord{
		focus(utc(2026, 6, 10, 9, 5), 25, true, "completed"),   // 9:05–9:30
		focus(utc(2026, 6, 10, 9, 35), 25, true, "completed"),  // 9:35–10:00 (gap 5m → bridge)
		focus(utc(2026, 6, 10, 10, 40), 80, true, "completed"), // 10:40–12:00 (gap 40m → split)
	}
	sessions := WorkSessions(recs, 15*time.Minute)
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", len(sessions), sessions)
	}
	s0 := sessions[0]
	if !s0.Start.Equal(utc(2026, 6, 10, 9, 5)) || !s0.End.Equal(utc(2026, 6, 10, 10, 0)) {
		t.Errorf("session0 bounds = %v..%v", s0.Start, s0.End)
	}
	if s0.Blocks != 2 || s0.ActiveSec != 50*60 {
		t.Errorf("session0 blocks=%d active=%d", s0.Blocks, s0.ActiveSec)
	}
	if s0.SpanSec() != 55*60 {
		t.Errorf("session0 span = %d, want %d", s0.SpanSec(), 55*60)
	}
	if s0.BreakSec() != 5*60 {
		t.Errorf("session0 break = %d, want %d", s0.BreakSec(), 5*60)
	}
}

func TestWorkSessionsUnordered(t *testing.T) {
	// Out-of-order input must still sessionize correctly.
	recs := []PhaseRecord{
		focus(utc(2026, 6, 10, 9, 35), 25, true, "completed"),
		focus(utc(2026, 6, 10, 9, 5), 25, true, "completed"),
	}
	sessions := WorkSessions(recs, 15*time.Minute)
	if len(sessions) != 1 || sessions[0].Blocks != 2 {
		t.Fatalf("want 1 session of 2 blocks, got %+v", sessions)
	}
}

func TestDayWorkSummary(t *testing.T) {
	recs := []PhaseRecord{
		focus(utc(2026, 6, 10, 9, 5), 25, true, "completed"),
		focus(utc(2026, 6, 10, 9, 35), 25, true, "completed"),
		focus(utc(2026, 6, 10, 10, 40), 80, true, "completed"),
		// previous day — must be excluded
		focus(utc(2026, 6, 9, 14, 0), 25, true, "completed"),
	}
	d := DayWork(recs, utc(2026, 6, 10, 12, 0), 15*time.Minute, 0, time.UTC)
	if d.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", d.Sessions)
	}
	if !d.WorkStart.Equal(utc(2026, 6, 10, 9, 5)) || !d.WorkEnd.Equal(utc(2026, 6, 10, 12, 0)) {
		t.Errorf("work span = %v..%v", d.WorkStart, d.WorkEnd)
	}
	if d.ActiveSec != (50+80)*60 {
		t.Errorf("active = %d, want %d", d.ActiveSec, (50+80)*60)
	}
	if d.LongestSec != 80*60 {
		t.Errorf("longest = %d, want %d", d.LongestSec, 80*60)
	}
	if d.Date != "2026-06-10" {
		t.Errorf("date = %q", d.Date)
	}
}

func TestWeekdayHourHeatmap(t *testing.T) {
	// 2026-06-10 is a Wednesday (weekday 3).
	recs := []PhaseRecord{
		focus(utc(2026, 6, 10, 9, 0), 25, true, "completed"),
		focus(utc(2026, 6, 10, 9, 30), 25, true, "completed"), // same hour bucket
		focus(utc(2026, 6, 10, 14, 0), 25, false, "stopped"),  // abandoned → ignored
	}
	h := WeekdayHourHeatmap(recs, time.UTC)
	if got := h[3][9]; got != 50 {
		t.Errorf("Wed 09:00 = %d min, want 50", got)
	}
	if got := h[3][14]; got != 0 {
		t.Errorf("abandoned focus should not count, got %d", got)
	}
}

func TestStreaksWithGrace(t *testing.T) {
	mk := func(days ...string) map[string]bool {
		m := map[string]bool{}
		for _, d := range days {
			m[d] = true
		}
		return m
	}
	today := utc(2026, 6, 10, 8, 0) // Wed

	// Strict: today + 2 prior days, then a gap.
	active := mk("2026-06-10", "2026-06-09", "2026-06-08", "2026-06-06")
	s := Streaks(active, today, 0, 0)
	if s.Current != 3 {
		t.Errorf("strict current = %d, want 3", s.Current)
	}

	// Grace 1: the 06-07 gap is forgiven, so 06-06 joins → current 4.
	s = Streaks(active, today, 0, 1)
	if s.Current != 4 {
		t.Errorf("grace current = %d, want 4", s.Current)
	}

	// Longest run anywhere in the set (08,09,10 = 3).
	if s.Longest != 3 {
		t.Errorf("longest = %d, want 3", s.Longest)
	}

	// No activity today, grace 0 → current 0.
	noToday := mk("2026-06-09", "2026-06-08")
	if got := Streaks(noToday, today, 0, 0).Current; got != 0 {
		t.Errorf("inactive today strict = %d, want 0", got)
	}
	// No activity today but grace 1 → yesterday's run counts.
	if got := Streaks(noToday, today, 0, 1).Current; got != 2 {
		t.Errorf("inactive today grace1 = %d, want 2", got)
	}
}

func TestDayStartHourShiftsBoundary(t *testing.T) {
	loc := time.UTC
	// A focus block at 01:30 on the 11th. With dayStartHour=4 it belongs to the
	// logical day of the 10th (the night-owl session).
	recs := []PhaseRecord{focus(utc(2026, 6, 11, 1, 30), 25, true, "completed")}

	naive := ActiveFocusDays(recs, 0, loc)
	if !naive["2026-06-11"] {
		t.Errorf("naive should bucket to the 11th, got %v", naive)
	}
	shifted := ActiveFocusDays(recs, 4, loc)
	if !shifted["2026-06-10"] || shifted["2026-06-11"] {
		t.Errorf("dayStart=4 should bucket 01:30 to the 10th, got %v", shifted)
	}

	// And the streak anchored at 02:00 on the 11th still counts that block as
	// "today" (the logical 10th).
	if got := Streaks(shifted, utc(2026, 6, 11, 2, 0), 4, 0).Current; got != 1 {
		t.Errorf("night-owl streak current = %d, want 1", got)
	}
}

func TestRollup(t *testing.T) {
	recs := []PhaseRecord{
		focus(utc(2026, 6, 8, 9, 0), 25, true, "completed"),  // Mon, 2026-W24
		focus(utc(2026, 6, 10, 9, 0), 25, true, "completed"), // Wed, 2026-W24
		focus(utc(2026, 6, 15, 9, 0), 50, true, "completed"), // Mon, 2026-W25
		focus(utc(2026, 6, 15, 10, 0), 25, false, "stopped"), // abandoned → ignored
	}
	weekly := Rollup(recs, GranWeek, 0, time.UTC)
	if len(weekly) != 2 {
		t.Fatalf("weekly buckets = %d, want 2: %+v", len(weekly), weekly)
	}
	// Buckets are returned in chronological order.
	if weekly[0].Key != "2026-W24" || weekly[0].Sessions != 2 || weekly[0].FocusMin != 50 {
		t.Errorf("week0 = %+v", weekly[0])
	}
	if weekly[1].Key != "2026-W25" || weekly[1].Sessions != 1 || weekly[1].FocusMin != 50 {
		t.Errorf("week1 = %+v", weekly[1])
	}

	monthly := Rollup(recs, GranMonth, 0, time.UTC)
	if len(monthly) != 1 || monthly[0].Key != "2026-06" || monthly[0].Sessions != 3 {
		t.Errorf("monthly = %+v", monthly)
	}
}

func TestActivityBetweenRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/a.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	base := utc(2026, 6, 10, 9, 0)
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(st.RecordActivity(base, "Claude", "claude", "Claude/claude/s1", "running"))
	must(st.RecordActivity(base.Add(time.Minute), "Codex", "codex", "Codex/codex/s2", "waiting"))
	must(st.RecordActivity(base.Add(48*time.Hour), "Claude", "claude", "Claude/claude/s1", "running")) // outside

	got, err := st.ActivityBetween(base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows in window, got %d", len(got))
	}
	if got[0].SessionKey != "Claude/claude/s1" || got[0].State != "running" || got[0].Tool != "claude" {
		t.Errorf("row0 = %+v", got[0])
	}
	if got[0].At.Unix() != base.Unix() {
		t.Errorf("row0 ts = %d, want %d", got[0].At.Unix(), base.Unix())
	}
}

func TestPhasesBetweenRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/a.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rec := func(start time.Time, durMin int) {
		r := PhaseResult{Phase: PhaseFocus, PlannedSec: durMin * 60, ActualSec: durMin * 60,
			Completed: true, Reason: "completed"}
		if err := st.RecordPhase(r, start, start.Add(time.Duration(durMin)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	rec(utc(2026, 6, 10, 9, 0), 25)
	rec(utc(2026, 6, 10, 10, 0), 25)
	rec(utc(2026, 6, 11, 9, 0), 25) // outside the window below

	got, err := st.PhasesBetween(utc(2026, 6, 10, 0, 0), utc(2026, 6, 11, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows in window, got %d", len(got))
	}
	if got[0].Phase != PhaseFocus || got[0].ActualSec != 25*60 || !got[0].Completed {
		t.Errorf("row0 decoded wrong: %+v", got[0])
	}
	if !got[0].StartedAt.Equal(utc(2026, 6, 10, 9, 0).Local()) &&
		got[0].StartedAt.Unix() != utc(2026, 6, 10, 9, 0).Unix() {
		t.Errorf("row0 start unix = %d, want %d", got[0].StartedAt.Unix(), utc(2026, 6, 10, 9, 0).Unix())
	}
}
