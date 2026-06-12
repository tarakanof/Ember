package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
)

// meetingCoordFixture builds a coordinator + recordingPublisher + meetingsStore
// with meetings enabled and a fresh store (lastFetchOK = now).
// cfg.Meetings.TileLeadMinutes = leadMinutes.
func meetingCoordFixture(t *testing.T, now time.Time, leadMinutes int) (*coordinator, *recordingPublisher, *meetingsStore) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Meetings.applyDefaults()
	cfg.Meetings.Enabled = true
	cfg.Meetings.TileLeadMinutes = leadMinutes
	app := NewApp(cfg, pub, testLogger())
	c := app.coord

	// Seed the meetingsStore as fresh.
	app.meetings.mu.Lock()
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	return c, pub, app.meetings
}

// seedMeeting adds a single future occurrence to the store.
func seedMeeting(s *meetingsStore, occ meetings.Occurrence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upcoming = []meetings.Occurrence{occ}
}

// TestMeetingTilePushedInsideWindow: meeting now+30m, lead 60, fresh store
// → CustomApp("ember-meet") with text "STANDUP 30m".
func TestMeetingTilePushedInsideWindow(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid1",
		Title: "STANDUP",
		Start: now.Add(30 * time.Minute),
		End:   now.Add(45 * time.Minute),
	})

	c.reconcileMeetingApp(now)

	names := pub.CustomNamesSnapshot()
	if len(names) != 1 || names[0] != "ember-meet" {
		t.Fatalf("expected one ember-meet push, got %v", names)
	}
	apps := pub.CustomAppsSnapshot()
	if len(apps) != 1 {
		t.Fatalf("expected one payload, got %d", len(apps))
	}
	wantText := "STANDUP 30m"
	if got := apps[0]["text"]; got != wantText {
		t.Errorf("payload text = %q, want %q", got, wantText)
	}
}

// TestMeetingTileAbsentOutsideWindow: meeting now+90m, lead 60 → no CustomApp call.
func TestMeetingTileAbsentOutsideWindow(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid2",
		Title: "STANDUP",
		Start: now.Add(90 * time.Minute),
		End:   now.Add(105 * time.Minute),
	})

	c.reconcileMeetingApp(now)

	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("outside window: want 0 CustomApp calls, got %d", got)
	}
}

// TestMeetingTileCountdownRepush: reconcile at T (pushes "30m"), reconcile
// again at T+1m → second CustomApp with "29m" (payload diff overrides the
// refresh-interval skip).
func TestMeetingTileCountdownRepush(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	start := now.Add(30 * time.Minute)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid3",
		Title: "STANDUP",
		Start: start,
		End:   start.Add(30 * time.Minute),
	})

	c.reconcileMeetingApp(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("first reconcile: want 1 push, got %d", got)
	}
	text1 := pub.CustomAppsSnapshot()[0]["text"]
	if text1 != "STANDUP 30m" {
		t.Errorf("first push text = %q, want %q", text1, "STANDUP 30m")
	}

	// Advance 1 minute — payload text changes → must re-push despite fresh push.
	// Also update lastFetchOK to stay fresh.
	now2 := now.Add(time.Minute)
	store.mu.Lock()
	store.lastFetchOK = now2
	store.mu.Unlock()
	c.reconcileMeetingApp(now2)

	apps := pub.CustomAppsSnapshot()
	if len(apps) != 2 {
		t.Fatalf("second reconcile: want 2 pushes total, got %d", len(apps))
	}
	text2 := apps[1]["text"]
	if text2 != "STANDUP 29m" {
		t.Errorf("second push text = %q, want %q", text2, "STANDUP 29m")
	}
}

// TestMeetingTileClearsAtStart: pushed; then now > meeting start (no later
// meeting) → ClearApp("ember-meet") once, pushedMeeting nil; a further
// reconcile does NOT ClearApp again.
func TestMeetingTileClearsAtStart(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	start := now.Add(30 * time.Minute)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid4",
		Title: "STANDUP",
		Start: start,
		End:   start.Add(30 * time.Minute),
	})

	c.reconcileMeetingApp(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	// Move past meeting start; no upcoming meetings left.
	nowPast := start.Add(time.Second)
	store.mu.Lock()
	store.upcoming = nil
	store.lastFetchOK = nowPast
	store.mu.Unlock()

	c.reconcileMeetingApp(nowPast)
	cleared := pub.ClearedAppsSnapshot()
	if len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("past start: want ClearApp(ember-meet), got %v", cleared)
	}
	c.muTest.RLock()
	pm := c.pushedMeeting
	c.muTest.RUnlock()
	if pm != nil {
		t.Error("pushedMeeting should be nil after clear")
	}

	// Second reconcile must not ClearApp again.
	c.reconcileMeetingApp(nowPast.Add(time.Minute))
	if got := len(pub.ClearedAppsSnapshot()); got != 1 {
		t.Errorf("second reconcile: want still 1 clear, got %d", got)
	}
}

// TestMeetingTileClearsWhenStale: pushed; then lastFetchOK aged 61m → ClearApp.
func TestMeetingTileClearsWhenStale(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	start := now.Add(30 * time.Minute)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid5",
		Title: "STANDUP",
		Start: start,
		End:   start.Add(30 * time.Minute),
	})

	c.reconcileMeetingApp(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	// Age the lastFetchOK past meetingsStaleTTL (60 min).
	stalePast := now.Add(meetingsStaleTTL + time.Minute)
	// Note: lastFetchOK stays at `now`, so fresh(stalePast) = false.
	c.reconcileMeetingApp(stalePast)

	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("stale feed: want ClearApp(ember-meet), got %v", cleared)
	}
}

// TestMeetingTileClearsWhenDisabled: pushed; cfg.Meetings.Enabled=false → ClearApp.
func TestMeetingTileClearsWhenDisabled(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	start := now.Add(30 * time.Minute)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid6",
		Title: "STANDUP",
		Start: start,
		End:   start.Add(30 * time.Minute),
	})

	c.reconcileMeetingApp(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	// Disable meetings.
	cfg := *c.loadCfg()
	cfg.Meetings.Enabled = false
	c.loadCfg = func() *Config { return &cfg }

	c.reconcileMeetingApp(now)
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("disabled: want ClearApp(ember-meet), got %v", cleared)
	}
}

// TestMeetingTileAdopted: device app loop contains "ember-meet" →
// adoptDeviceManagedApps seeds pushedMeeting non-nil.
func TestMeetingTileAdopted(t *testing.T) {
	pub := &recordingPublisher{loopApps: []string{
		"Time", "ember", "ember-weather", "ember-meet",
	}}
	cfg := defaultConfig()
	cfg.Meetings.applyDefaults()
	cfg.Meetings.Enabled = false // disable so the tile is cleared
	app := NewApp(cfg, pub, testLogger())
	c := app.coord

	if !c.adoptDeviceManagedApps() {
		t.Fatal("adopt should succeed when device loop is readable")
	}
	c.muTest.RLock()
	pm := c.pushedMeeting
	c.muTest.RUnlock()
	if pm == nil {
		t.Fatal("pushedMeeting should be seeded non-nil after adopt")
	}

	// With meetings disabled, reconcile should ClearApp.
	now := time.Now()
	c.reconcileMeetingApp(now)
	cleared := pub.ClearedAppsSnapshot()
	found := false
	for _, name := range cleared {
		if name == "ember-meet" {
			found = true
		}
	}
	if !found {
		t.Errorf("after adopt+disabled, want ClearApp(ember-meet), got %v", cleared)
	}
}

// TestMeetingMinutes: table tests for meetingMinutes ceil semantics.
func TestMeetingMinutes(t *testing.T) {
	base := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		remaining time.Duration
		want      int
		desc      string
	}{
		{30 * time.Minute, 30, "30m0s → 30"},
		{29*time.Minute + 1*time.Second, 30, "29m1s → ceil 30"},
		{59 * time.Second, 1, "59s → 1"},
		{1 * time.Second, 1, "1s → 1 (min 1)"},
		{60 * time.Minute, 60, "exactly 60m → 60"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("remaining=%v", tc.remaining), func(t *testing.T) {
			start := base.Add(tc.remaining)
			got := meetingMinutes(base, start)
			if got != tc.want {
				t.Errorf("%s: meetingMinutes = %d, want %d", tc.desc, got, tc.want)
			}
		})
	}
}
