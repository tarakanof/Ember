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
