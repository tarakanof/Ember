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

	_ = s.RecordPhase(PhaseResult{Phase: PhaseFocus, PlannedSec: 1500, ActualSec: 120, Completed: false, Reason: "stopped"}, now.Add(-2*time.Minute), now)
	_ = s.RecordPhase(PhaseResult{Phase: PhaseShort, PlannedSec: 300, ActualSec: 300, Completed: true, Reason: "completed"}, now.Add(-5*time.Minute), now)

	day, err := s.Today(now)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if day.CompletedFocus != 0 {
		t.Fatalf("CompletedFocus = %d, want 0", day.CompletedFocus)
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
	if err := s.PutSetting("focus_min", "45"); err != nil {
		t.Fatalf("PutSetting overwrite: %v", err)
	}
	val, _, _ = s.GetSetting("focus_min")
	if val != "45" {
		t.Fatalf("value after overwrite = %q, want 45", val)
	}
}

func TestRecordPhaseAdvancesPhaseGen(t *testing.T) {
	s := openTestStore(t)
	before := s.PhaseGen()
	now := time.Now()
	if err := s.RecordPhase(PhaseResult{Phase: PhaseFocus, PlannedSec: 60, ActualSec: 60, Completed: true, Reason: "completed"},
		now.Add(-time.Minute), now); err != nil {
		t.Fatalf("RecordPhase: %v", err)
	}
	if after := s.PhaseGen(); after == before {
		t.Fatalf("PhaseGen unchanged after RecordPhase (%d)", after)
	}
}

func TestSettingWriteLeavesPhaseGenAlone(t *testing.T) {
	s := openTestStore(t)
	before := s.PhaseGen()
	if err := s.PutSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	if after := s.PhaseGen(); after != before {
		t.Fatalf("PhaseGen changed on a settings write: %d -> %d", before, after)
	}
}

func TestPutSettingsWritesAll(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutSettings(map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"a": "1", "b": "2"} {
		if got, ok, _ := s.GetSetting(k); !ok || got != want {
			t.Fatalf("%s = %q, %v", k, got, ok)
		}
	}
}

func TestPutSettingsRollsBackOnFailure(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_b BEFORE INSERT ON settings WHEN NEW.key = 'b' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSettings(map[string]string{"a": "1", "b": "2", "c": "3"}); err == nil {
		t.Fatal("PutSettings succeeded")
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok, _ := s.GetSetting(k); ok {
			t.Fatalf("%s stored after a failed batch", k)
		}
	}
}

func TestOpenSetsWALSafePragmas(t *testing.T) {
	s := openTestStore(t)
	var sync, busy int
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("synchronous: %v", err)
	}
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if sync != 1 || busy != 2000 {
		t.Fatalf("synchronous=%d busy_timeout=%d, want 1 and 2000", sync, busy)
	}
}
