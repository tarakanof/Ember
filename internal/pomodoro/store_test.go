package pomodoro

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pomo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordedFocusShowsInToday(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	start := now.Add(-25 * time.Minute)

	err := s.RecordPhase(PhaseResult{
		Phase: PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed",
	}, start, now)
	if err != nil {
		t.Fatalf("RecordPhase: %v", err)
	}

	day, err := s.Today(now)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if day.CompletedFocus != 1 {
		t.Fatalf("CompletedFocus = %d, want 1", day.CompletedFocus)
	}
	if day.FocusMin != 25 {
		t.Fatalf("FocusMin = %d, want 25", day.FocusMin)
	}
}

func TestIncompleteAndBreakPhasesDoNotCountAsFocus(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)

	// A stopped focus phase: doesn't count toward completed focus.
	_ = s.RecordPhase(PhaseResult{Phase: PhaseFocus, PlannedSec: 1500, ActualSec: 120, Completed: false, Reason: "stopped"}, now.Add(-2*time.Minute), now)
	// A completed break: not a focus phase.
	_ = s.RecordPhase(PhaseResult{Phase: PhaseShort, PlannedSec: 300, ActualSec: 300, Completed: true, Reason: "completed"}, now.Add(-5*time.Minute), now)

	day, err := s.Today(now)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if day.CompletedFocus != 0 {
		t.Fatalf("CompletedFocus = %d, want 0", day.CompletedFocus)
	}
}

func TestStreakCountsConsecutiveDaysAndBreaksOnGap(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 5, 28, 23, 0, 0, 0, time.UTC)
	rec := func(daysAgo int) {
		end := now.AddDate(0, 0, -daysAgo)
		start := end.Add(-25 * time.Minute)
		if err := s.RecordPhase(PhaseResult{Phase: PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed"}, start, end); err != nil {
			t.Fatalf("RecordPhase: %v", err)
		}
	}
	// Today, yesterday, 2 days ago → streak 3. Gap at 3 days ago. 4 days ago present but unreachable.
	rec(0)
	rec(1)
	rec(2)
	rec(4)

	streak, err := s.Streak(now)
	if err != nil {
		t.Fatalf("Streak: %v", err)
	}
	if streak != 3 {
		t.Fatalf("streak = %d, want 3", streak)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if _, ok, _ := s.GetSetting("focus_min"); ok {
		t.Fatal("expected missing setting before write")
	}
	if err := s.PutSetting("focus_min", "30"); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	val, ok, err := s.GetSetting("focus_min")
	if err != nil || !ok {
		t.Fatalf("GetSetting err=%v ok=%v", err, ok)
	}
	if val != "30" {
		t.Fatalf("value = %q, want 30", val)
	}
	// Overwrite.
	if err := s.PutSetting("focus_min", "45"); err != nil {
		t.Fatalf("PutSetting overwrite: %v", err)
	}
	val, _, _ = s.GetSetting("focus_min")
	if val != "45" {
		t.Fatalf("value after overwrite = %q, want 45", val)
	}
}

func TestHistoryReturnsPerDayRollup(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	mk := func(daysAgo, n int) {
		for i := 0; i < n; i++ {
			end := now.AddDate(0, 0, -daysAgo)
			_ = s.RecordPhase(PhaseResult{Phase: PhaseFocus, PlannedSec: 1500, ActualSec: 1500, Completed: true, Reason: "completed"}, end.Add(-25*time.Minute), end)
		}
	}
	mk(0, 2)
	mk(1, 1)

	hist, err := s.History(now, 3)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	// Most recent first.
	if hist[0].CompletedFocus != 2 || hist[1].CompletedFocus != 1 || hist[2].CompletedFocus != 0 {
		t.Fatalf("history counts = %d,%d,%d, want 2,1,0", hist[0].CompletedFocus, hist[1].CompletedFocus, hist[2].CompletedFocus)
	}
}
