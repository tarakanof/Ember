package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
)

// singleEventICS returns an ICS calendar with one event starting at `start`
// and lasting 30 minutes.
func singleEventICS(uid, summary string, start time.Time) []byte {
	return []byte(fmt.Sprintf(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//ember-test//EN
BEGIN:VEVENT
UID:%s
DTSTART:%s
DTEND:%s
SUMMARY:%s
END:VEVENT
END:VCALENDAR
`, uid, start.UTC().Format("20060102T150405Z"), start.UTC().Add(30*time.Minute).Format("20060102T150405Z"), summary))
}

// newMeetingsTestApp creates a minimal App for meetings tests: config with
// Meetings enabled, popup lead 2 min, chime on; no database store (no persisted
// settings); fake publisher.
func newMeetingsTestApp(t *testing.T, pub *recordingPublisher) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Meetings.Enabled = true
	cfg.Meetings.TileLeadMinutes = 60
	cfg.Meetings.PopupLeadMinutes = 2
	cfg.Meetings.Chime = true
	app := NewApp(cfg, pub, testLogger())
	return app
}

func TestPollMeetingsFetchesAndStores(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	icsData := singleEventICS("event1@test", "Standup", now.Add(30*time.Minute))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		w.Write(icsData)
	}))
	defer srv.Close()

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{srv.URL}
	app.meetingsFetcher = newICSFetcher()

	app.pollMeetings(context.Background(), now)

	app.meetings.mu.RLock()
	upcoming := app.meetings.upcoming
	lastFetchOK := app.meetings.lastFetchOK
	app.meetings.mu.RUnlock()

	if len(upcoming) == 0 {
		t.Fatal("upcoming should be non-empty after successful fetch")
	}
	if !app.meetings.fresh(now) {
		t.Errorf("fresh(now) should be true after successful fetch; lastFetchOK=%v", lastFetchOK)
	}
}

func TestPollMeetingsDueGate(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
		w.Write(singleEventICS("evt@test", "Test", now.Add(30*time.Minute)))
	}))
	defer srv.Close()

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{srv.URL}
	app.meetingsFetcher = newICSFetcher()

	// First poll: should fetch.
	app.pollMeetings(context.Background(), now)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("first poll: hits=%d want 1", got)
	}

	// Second poll at now+1m: inside the 5-min gate, no fetch.
	app.pollMeetings(context.Background(), now.Add(time.Minute))
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("within interval: hits=%d want 1 (due-gate should suppress)", got)
	}

	// Third poll at now+5m: exactly at boundary — should fetch again.
	app.pollMeetings(context.Background(), now.Add(meetingsRefreshInterval))
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("after refresh interval: hits=%d want 2", got)
	}
}

func TestPollMeetingsPartialFailure(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	icsData := singleEventICS("good@test", "Good Meeting", now.Add(30*time.Minute))

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(icsData)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvB.Close()

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{srvA.URL, srvB.URL}
	app.meetingsFetcher = newICSFetcher()

	app.pollMeetings(context.Background(), now)

	app.meetings.mu.RLock()
	upcoming := app.meetings.upcoming
	lastFetchOK := app.meetings.lastFetchOK
	app.meetings.mu.RUnlock()

	// A's occurrences should be stored.
	if len(upcoming) == 0 {
		t.Fatal("partial success: upcoming should be non-empty (URL A succeeded)")
	}
	// lastFetchOK must be advanced (at least one success).
	if lastFetchOK.IsZero() {
		t.Error("partial success: lastFetchOK should be advanced")
	}
}

func TestPollMeetingsAllFail(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvB.Close()

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{srvA.URL, srvB.URL}
	app.meetingsFetcher = newICSFetcher()

	// Seed store with a previous occurrence and an old lastFetchOK.
	oldFetchOK := now.Add(-10 * time.Minute)
	prevOcc := meetings.Occurrence{
		UID:   "prev@test",
		Title: "Previous Meeting",
		Start: now.Add(20 * time.Minute),
		End:   now.Add(50 * time.Minute),
	}
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{prevOcc}
	app.meetings.lastFetchOK = oldFetchOK
	app.meetings.mu.Unlock()

	app.pollMeetings(context.Background(), now)

	app.meetings.mu.RLock()
	upcoming := app.meetings.upcoming
	lastFetchOK := app.meetings.lastFetchOK
	app.meetings.mu.RUnlock()

	// Upcoming must be retained (not cleared on all-fail).
	if len(upcoming) == 0 {
		t.Fatal("all-fail: previously stored occurrences must be retained")
	}
	// lastFetchOK must NOT be advanced.
	if !lastFetchOK.Equal(oldFetchOK) {
		t.Errorf("all-fail: lastFetchOK should stay at %v, got %v", oldFetchOK, lastFetchOK)
	}
}

func TestPollMeetingsErrorNeverContainsURL(t *testing.T) {
	// Create a listener, immediately close it, so connections will be refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	closedAddr := "http://" + ln.Addr().String() + "/feed.ics"
	ln.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := defaultConfig()
	cfg.Meetings.Enabled = true
	cfg.Meetings.TileLeadMinutes = 60
	cfg.Meetings.PopupLeadMinutes = 2
	cfg.Meetings.Chime = true
	app := NewApp(cfg, &recordingPublisher{}, logger)
	app.meetingsURLs = []string{closedAddr}
	app.meetingsFetcher = newICSFetcher()

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	app.pollMeetings(context.Background(), now)

	logOutput := logBuf.String()
	// The URL (or its host) must not appear in any log output.
	host := ln.Addr().String()
	if strings.Contains(logOutput, host) {
		t.Errorf("log output contains URL host %q (secret hygiene violation):\n%s", host, logOutput)
	}
	if strings.Contains(logOutput, closedAddr) {
		t.Errorf("log output contains full URL (secret hygiene violation):\n%s", logOutput)
	}
}

func TestMeetingPopupFiresOnceInWindow(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub) // chime=true, lead=2

	// Seed the store: meeting starts at now+2m, feed is fresh.
	meetStart := now.Add(2 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "standup@test",
		Title: "Standup",
		Start: meetStart,
		End:   meetStart.Add(15 * time.Minute),
	}}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings

	// First call: should fire popup and chime.
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount1 := len(pub.notify)
	rtttlCount1 := len(pub.rtttls)
	pub.mu.Unlock()

	if notifyCount1 != 1 {
		t.Fatalf("first checkMeetingPopup: Notify called %d times, want 1", notifyCount1)
	}
	if rtttlCount1 != 1 {
		t.Fatalf("first checkMeetingPopup: PlayRTTTL called %d times, want 1 (chime=true)", rtttlCount1)
	}

	// Check popup text contains "STANDUP IN 2M".
	pub.mu.Lock()
	notifyPayload := pub.notify[0]
	pub.mu.Unlock()
	if text, _ := notifyPayload["text"].(string); text != "STANDUP IN 2M" {
		t.Errorf("popup text = %q, want %q", text, "STANDUP IN 2M")
	}

	// Second call at now+30s (still in window): must NOT fire again.
	app.checkMeetingPopup(context.Background(), now.Add(30*time.Second), cfg)

	pub.mu.Lock()
	notifyCount2 := len(pub.notify)
	pub.mu.Unlock()

	if notifyCount2 != 1 {
		t.Errorf("second call in window: Notify called %d times total, want 1 (dedupe)", notifyCount2)
	}
}

func TestMeetingPopupNoChimeWhenOff(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)

	// Override chime to false.
	cur := *app.cfg.Load()
	cur.Meetings.Chime = false
	app.cfg.Store(&cur)

	meetStart := now.Add(2 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "meet@test",
		Title: "Meeting",
		Start: meetStart,
		End:   meetStart.Add(30 * time.Minute),
	}}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	rtttlCount := len(pub.rtttls)
	pub.mu.Unlock()

	if notifyCount != 1 {
		t.Errorf("chime=false: Notify count = %d, want 1", notifyCount)
	}
	if rtttlCount != 0 {
		t.Errorf("chime=false: PlayRTTTL count = %d, want 0", rtttlCount)
	}
}

func TestMeetingPopupSkippedWhenLeadZero(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)

	cur := *app.cfg.Load()
	cur.Meetings.PopupLeadMinutes = 0
	app.cfg.Store(&cur)

	meetStart := now.Add(2 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "meet@test",
		Title: "Meeting",
		Start: meetStart,
		End:   meetStart.Add(30 * time.Minute),
	}}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	pub.mu.Unlock()

	if notifyCount != 0 {
		t.Errorf("lead=0: Notify count = %d, want 0", notifyCount)
	}
}

func TestMeetingPopupSkippedWhenStale(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)

	meetStart := now.Add(2 * time.Minute)
	// lastFetchOK is 61 minutes ago (beyond meetingsStaleTTL = 60m).
	staleFetch := now.Add(-61 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "meet@test",
		Title: "Meeting",
		Start: meetStart,
		End:   meetStart.Add(30 * time.Minute),
	}}
	app.meetings.lastFetchOK = staleFetch
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	pub.mu.Unlock()

	if notifyCount != 0 {
		t.Errorf("stale feed: Notify count = %d, want 0", notifyCount)
	}
}

func TestMeetingPopupGraceExpired(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub) // lead=2, grace=2m

	// Meeting should have fired 2m+3m=5m ago (3m past grace window).
	meetStart := now.Add(-1 * time.Minute) // started 1m ago
	// fireAt = meetStart - 2m = now - 3m
	// now.Sub(fireAt) = 3m >= meetingPopupGrace (2m) → should NOT fire
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "past@test",
		Title: "Past Meeting",
		Start: meetStart,
		End:   meetStart.Add(30 * time.Minute),
	}}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	pub.mu.Unlock()

	if notifyCount != 0 {
		t.Errorf("grace expired: Notify count = %d, want 0", notifyCount)
	}
}

func TestNextOccurrenceSkipsStarted(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 5, 0, 0, time.UTC) // 09:05

	store := newMeetingsStore()
	t9 := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	t10 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	store.upcoming = []meetings.Occurrence{
		{UID: "early@test", Title: "Early", Start: t9, End: t9.Add(30 * time.Minute)},
		{UID: "late@test", Title: "Late", Start: t10, End: t10.Add(30 * time.Minute)},
	}

	occ, ok := store.next(now)
	if !ok {
		t.Fatal("next() returned ok=false; expected the 10:00 meeting")
	}
	if occ.UID != "late@test" {
		t.Errorf("next() returned UID=%q, want %q", occ.UID, "late@test")
	}
}

func TestSanitizeMeetingTitle(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Standup", "STANDUP"},
		{"Café sync!", "CAF SYNC"},
		{"", "MEETING"},
		{"This title is way too long and should be truncated to 24 runes!", "THIS TITLE IS WAY TOO LO"},
		// Finding 3: if the 24th rune is a space the trailing space must be stripped.
		// "ABCDEFGHIJ KLMNOPQRSTU VW" has a space at index 21; pad to make the
		// cut land exactly on a space: 23 non-space chars + space at position 23 (0-indexed).
		// Simplest: 23 'A's followed by a space followed by more text → cap gives "AAAAAAAAAAAAAAAAAAAAAA A"
		// (23 A's + space = 24 runes) → TrimRight should yield 23 A's.
		{"AAAAAAAAAAAAAAAAAAAAAAA XYZ", "AAAAAAAAAAAAAAAAAAAAAAA"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitizeMeetingTitle(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeMeetingTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestMeetingPopupBackToBackMeetings verifies that checkMeetingPopup fires a
// popup for every upcoming occurrence whose lead window contains now, not only
// the first one returned by next().
//
// The classic missed-popup scenario: A at now+2m and B at now+3m with lead=2m.
//
//	A: fireAt = now,   window = [now,   now+2m)
//	B: fireAt = now+1m, window = [now+1m, now+3m)
//
// At tick 0 (now): A fires. next() returns A (Start=now+2m > now), so A is
// checked and fires — B's window hasn't opened yet. Good.
//
// At tick 1 (now+1m): with the OLD single-next() code, next(now+1m) returns A
// again (A.Start=now+2m > now+1m). A is already deduped → nothing fires. B's
// window [now+1m, now+3m) contains now+1m and should fire, but it is never
// checked. This is the bug the loop over snapshot() fixes.
func TestMeetingPopupBackToBackMeetings(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	meetA := now.Add(2 * time.Minute) // fireAt_A = now
	meetB := now.Add(3 * time.Minute) // fireAt_B = now+1m

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub) // lead=2, chime=true
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{
		{UID: "meetA@test", Title: "Alpha", Start: meetA, End: meetA.Add(30 * time.Minute)},
		{UID: "meetB@test", Title: "Beta", Start: meetB, End: meetB.Add(30 * time.Minute)},
	}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings

	// Tick 0 (t=now): A's window [now, now+2m) contains now → A fires; B does not.
	app.checkMeetingPopup(context.Background(), now, cfg)
	pub.mu.Lock()
	notifyAt0 := len(pub.notify)
	pub.mu.Unlock()
	if notifyAt0 != 1 {
		t.Fatalf("tick 0: Notify count = %d, want 1 (A only)", notifyAt0)
	}
	pub.mu.Lock()
	txt0 := pub.notify[0]["text"]
	pub.mu.Unlock()
	if txt0 != "ALPHA IN 2M" {
		t.Errorf("tick 0 popup text = %q, want %q", txt0, "ALPHA IN 2M")
	}

	// Tick 1 (t=now+1m): A is deduped; B's window [now+1m, now+3m) contains now+1m → B fires.
	app.checkMeetingPopup(context.Background(), now.Add(time.Minute), cfg)
	pub.mu.Lock()
	notifyAt1 := len(pub.notify)
	pub.mu.Unlock()
	if notifyAt1 != 2 {
		t.Fatalf("tick 1: total Notify count = %d, want 2 (A deduped, B fires)", notifyAt1)
	}
	pub.mu.Lock()
	txt1 := pub.notify[1]["text"]
	pub.mu.Unlock()
	if txt1 != "BETA IN 2M" {
		t.Errorf("tick 1 popup text = %q, want %q", txt1, "BETA IN 2M")
	}

	// Sanity: no double-fire — another call at the same tick must not add more.
	app.checkMeetingPopup(context.Background(), now.Add(time.Minute), cfg)
	pub.mu.Lock()
	notifyAfterRedupe := len(pub.notify)
	pub.mu.Unlock()
	if notifyAfterRedupe != 2 {
		t.Errorf("dedupe: total Notify count = %d, want 2 (no double-fire)", notifyAfterRedupe)
	}
}

// TestMeetingPopupBothWindowsSameTick verifies that when two meetings' lead
// windows both contain now, a single checkMeetingPopup call fires both popups.
func TestMeetingPopupBothWindowsSameTick(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	// lead=2m: fireAt_A = now+2m-2m = now, fireAt_B = now+2m-2m = now (same start)
	meetStart := now.Add(2 * time.Minute)

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub) // lead=2, chime=true
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{
		{UID: "c1@test", Title: "Call One", Start: meetStart, End: meetStart.Add(30 * time.Minute)},
		{UID: "c2@test", Title: "Call Two", Start: meetStart, End: meetStart.Add(45 * time.Minute)},
	}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load().Meetings
	app.checkMeetingPopup(context.Background(), now, cfg)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	pub.mu.Unlock()
	if notifyCount != 2 {
		t.Fatalf("same-tick two-meeting: Notify count = %d, want 2", notifyCount)
	}
}

// TestPollMeetingsRefreshesBeforePopup verifies that on a due-fetch tick, the
// feed is fetched and merged BEFORE checkMeetingPopup evaluates, so a meeting
// that was removed from the calendar does not fire a popup from stale state.
//
// Setup: store contains M1 at now+2m (its popup window contains now with lead=2).
// lastFetch is zero (never fetched → due). The feed returns an empty VCALENDAR
// (successful fetch, zero occurrences). With the correct ordering (fetch first,
// then popup), upcoming becomes empty → no popup fires. With the old ordering
// (popup first, then fetch), M1 is still in upcoming when the popup check runs
// and fires once from stale state.
func TestPollMeetingsRefreshesBeforePopup(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	// Empty calendar — successful fetch that yields zero occurrences.
	emptyICS := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//ember-test//EN\r\nEND:VCALENDAR\r\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		w.Write(emptyICS)
	}))
	defer srv.Close()

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub) // lead=2, chime=true
	app.meetingsURLs = []string{srv.URL}
	app.meetingsFetcher = newICSFetcher()

	// Seed store: M1 at now+2m, feed fresh enough that fresh(now) returns true,
	// but lastFetch zero so a fetch is due.
	m1Start := now.Add(2 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{
		UID:   "m1@test",
		Title: "Cancelled Meeting",
		Start: m1Start,
		End:   m1Start.Add(30 * time.Minute),
	}}
	app.meetings.lastFetchOK = now // fresh (within 60-min staleness TTL)
	// lastFetch left at zero → fetch is due
	app.meetings.mu.Unlock()

	app.pollMeetings(context.Background(), now)

	pub.mu.Lock()
	notifyCount := len(pub.notify)
	pub.mu.Unlock()

	if notifyCount != 0 {
		t.Errorf("stale M1 popup fired despite fresh feed replacing it: Notify count = %d, want 0", notifyCount)
	}

	// Also verify that the store was actually cleared (empty fetch replaced M1).
	app.meetings.mu.RLock()
	upcomingLen := len(app.meetings.upcoming)
	app.meetings.mu.RUnlock()
	if upcomingLen != 0 {
		t.Errorf("upcoming after empty-feed fetch = %d occurrences, want 0", upcomingLen)
	}
}

// TestPollMeetingsPrunesFiredMap verifies the pruning logic inside pollMeetings:
// entries whose embedded start is >2h before now are removed; recent entries and
// malformed keys are retained.
func TestPollMeetingsPrunesFiredMap(t *testing.T) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	icsData := singleEventICS("prune-event@test", "Prune Test", now.Add(30*time.Minute))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		w.Write(icsData)
	}))
	defer srv.Close()

	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{srv.URL}
	app.meetingsFetcher = newICSFetcher()

	// Seed the fired map with three entries:
	//   (a) start 3h before now → must be pruned
	//   (b) start 10min before now → must be retained (within 2h)
	//   (c) malformed key (no "|") → must be retained (not crashed)
	old := now.Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	recent := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	keyOld := "uid-old@test|" + old
	keyRecent := "uid-recent@test|" + recent
	keyMalformed := "garbage"

	app.meetings.mu.Lock()
	app.meetings.fired[keyOld] = struct{}{}
	app.meetings.fired[keyRecent] = struct{}{}
	app.meetings.fired[keyMalformed] = struct{}{}
	app.meetings.mu.Unlock()

	app.pollMeetings(context.Background(), now)

	app.meetings.mu.RLock()
	_, hasOld := app.meetings.fired[keyOld]
	_, hasRecent := app.meetings.fired[keyRecent]
	_, hasMalformed := app.meetings.fired[keyMalformed]
	app.meetings.mu.RUnlock()

	if hasOld {
		t.Error("fired[keyOld] should have been pruned (start >2h before now)")
	}
	if !hasRecent {
		t.Error("fired[keyRecent] should be retained (start only 10min before now)")
	}
	if !hasMalformed {
		t.Error("fired[keyMalformed] should be retained (malformed keys are kept, not crashed)")
	}
}
