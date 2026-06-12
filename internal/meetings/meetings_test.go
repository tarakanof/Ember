package meetings_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
)

// mustLoad reads a fixture from testdata/ and returns its bytes.
func mustLoad(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("mustLoad %s: %v", name, err)
	}
	return data
}

func TestExpandWeeklyWithExdateAndOverride(t *testing.T) {
	from := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "standup_weekly.ics"), from, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// want, in order (Belgrade is UTC+2 in June):
	// Mon 08 07:30Z "Standup", Tue 09 07:30Z "Standup",
	// [Wed 10 EXDATE'd → absent],
	// Thu 11 12:00Z "Standup (moved)" (RECURRENCE-ID override),
	// Fri 12 07:30Z "Standup"
	want := []meetings.Occurrence{
		{
			UID:   "standup@test",
			Title: "Standup",
			Start: time.Date(2026, 6, 8, 7, 30, 0, 0, time.UTC),
			End:   time.Date(2026, 6, 8, 7, 45, 0, 0, time.UTC),
		},
		{
			UID:   "standup@test",
			Title: "Standup",
			Start: time.Date(2026, 6, 9, 7, 30, 0, 0, time.UTC),
			End:   time.Date(2026, 6, 9, 7, 45, 0, 0, time.UTC),
		},
		{
			UID:   "standup@test",
			Title: "Standup (moved)",
			Start: time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 6, 11, 12, 15, 0, 0, time.UTC),
		},
		{
			UID:   "standup@test",
			Title: "Standup",
			Start: time.Date(2026, 6, 12, 7, 30, 0, 0, time.UTC),
			End:   time.Date(2026, 6, 12, 7, 45, 0, 0, time.UTC),
		},
	}

	if len(occs) != len(want) {
		t.Fatalf("want %d occurrences, got %d: %v", len(want), len(occs), occs)
	}
	for i, w := range want {
		got := occs[i]
		if got.UID != w.UID {
			t.Errorf("[%d] UID: want %q, got %q", i, w.UID, got.UID)
		}
		if got.Title != w.Title {
			t.Errorf("[%d] Title: want %q, got %q", i, w.Title, got.Title)
		}
		if !got.Start.UTC().Equal(w.Start) {
			t.Errorf("[%d] Start: want %v, got %v", i, w.Start, got.Start.UTC())
		}
		if !got.End.UTC().Equal(w.End) {
			t.Errorf("[%d] End: want %v, got %v", i, w.End, got.End.UTC())
		}
	}
}

func TestExpandSingleEvent(t *testing.T) {
	from := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "single.ics"), from, 24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 1 {
		t.Fatalf("want 1 occurrence, got %d", len(occs))
	}
	occ := occs[0]
	if occ.UID != "oneoff@test" {
		t.Errorf("UID: want %q, got %q", "oneoff@test", occ.UID)
	}
	if occ.Title != "Dentist" {
		t.Errorf("Title: want %q, got %q", "Dentist", occ.Title)
	}
	wantStart := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 9, 13, 0, 0, 0, time.UTC)
	if !occ.Start.UTC().Equal(wantStart) {
		t.Errorf("Start: want %v, got %v", wantStart, occ.Start.UTC())
	}
	if !occ.End.UTC().Equal(wantEnd) {
		t.Errorf("End: want %v, got %v", wantEnd, occ.End.UTC())
	}
}

func TestExpandSkipsAllDay(t *testing.T) {
	from := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "allday.ics"), from, 24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 0 {
		t.Errorf("want 0 occurrences for all-day event, got %d: %v", len(occs), occs)
	}
}

func TestExpandSkipsCancelled(t *testing.T) {
	from := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "cancelled.ics"), from, 24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 0 {
		t.Errorf("want 0 occurrences for cancelled event, got %d: %v", len(occs), occs)
	}
}

func TestExpandHorizonBounds(t *testing.T) {
	// from AFTER the event → 0 results
	afterFrom := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "single.ics"), afterFrom, 24*time.Hour)
	if err != nil {
		t.Fatalf("Expand (after): %v", err)
	}
	if len(occs) != 0 {
		t.Errorf("from after event: want 0, got %d", len(occs))
	}

	// from far before but horizon ending before the event → 0 results
	// Event is at 2026-06-09 12:00Z; start from 2026-06-08, horizon 1h → window ends 2026-06-08T01:00Z
	earlyFrom := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	occs, err = meetings.Expand(mustLoad(t, "single.ics"), earlyFrom, time.Hour)
	if err != nil {
		t.Fatalf("Expand (before): %v", err)
	}
	if len(occs) != 0 {
		t.Errorf("horizon before event: want 0, got %d", len(occs))
	}
}

func TestExpandDSTBoundary(t *testing.T) {
	// Europe switched to CEST on 2026-03-29:
	// Mar 24 occurrence is 09:00 CET = 08:00 UTC
	// Mar 31 occurrence is 09:00 CEST = 07:00 UTC
	from := time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "dst.ics"), from, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 2 {
		t.Fatalf("want 2 occurrences, got %d: %v", len(occs), occs)
	}
	want := []time.Time{
		time.Date(2026, 3, 24, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 7, 0, 0, 0, time.UTC),
	}
	for i, w := range want {
		if !occs[i].Start.UTC().Equal(w) {
			t.Errorf("[%d] Start: want %v, got %v", i, w, occs[i].Start.UTC())
		}
	}
}

// TestExpandOverrideMovedBeforeWindow verifies that a RECURRENCE-ID override
// whose new DTSTART is before the poll window is excluded from results.
// The fixture has a daily event starting 2026-06-15; the Wed 2026-06-17
// instance is overridden to 2026-06-10 09:30 Belgrade (before window start).
// Polling from 2026-06-15 for 5 days should yield Mon/Tue/Thu/Fri but NOT
// the backward-moved occurrence.
func TestExpandOverrideMovedBeforeWindow(t *testing.T) {
	from := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	occs, err := meetings.Expand(mustLoad(t, "override_backward.ics"), from, 5*24*time.Hour)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// The Wed instance was moved to 2026-06-10 (before window), so it must be absent.
	// In-window occurrences: Mon 15, Tue 16, [Wed 17 overridden out], Thu 18, Fri 19.
	for _, occ := range occs {
		if occ.Title == "Weekly (moved back)" {
			t.Errorf("override moved before window must not appear, but got: %v", occ)
		}
		if occ.Start.Before(from) {
			t.Errorf("occurrence before window start: %v", occ)
		}
	}

	// Also assert the four un-overridden in-window occurrences are present.
	if len(occs) != 4 {
		t.Errorf("want 4 occurrences (Mon/Tue/Thu/Fri), got %d: %v", len(occs), occs)
	}
}

func TestMergeSortsAndDedupes(t *testing.T) {
	a := []meetings.Occurrence{
		{UID: "a@test", Title: "Alpha", Start: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 6, 9, 11, 0, 0, 0, time.UTC)},
		{UID: "b@test", Title: "Beta", Start: time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC), End: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)},
	}
	b := []meetings.Occurrence{
		// duplicate of a[0] — same UID and Start
		{UID: "a@test", Title: "Alpha", Start: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 6, 9, 11, 0, 0, 0, time.UTC)},
		{UID: "c@test", Title: "Gamma", Start: time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC), End: time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)},
	}

	merged := meetings.Merge(a, b)

	if len(merged) != 3 {
		t.Fatalf("want 3 (deduplicated), got %d: %v", len(merged), merged)
	}
	// Want sorted by Start: Gamma(8:00), Beta(9:00), Alpha(10:00)
	wantOrder := []string{"Gamma", "Beta", "Alpha"}
	for i, title := range wantOrder {
		if merged[i].Title != title {
			t.Errorf("[%d] want Title %q, got %q", i, title, merged[i].Title)
		}
	}
}
