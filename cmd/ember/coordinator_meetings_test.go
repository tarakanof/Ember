package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
)

func meetingCoordFixture(t *testing.T, now time.Time, leadMinutes int) (*coordinator, *recordingPublisher, *meetingsStore) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Meetings.applyDefaults()
	cfg.Meetings.Enabled = boolPtr(true)
	cfg.Meetings.TileLeadMinutes = leadMinutes
	app := NewApp(cfg, pub, testLogger())
	c := app.coord

	app.meetings.mu.Lock()
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()

	return c, pub, app.meetings
}

func seedMeeting(s *meetingsStore, occ meetings.Occurrence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upcoming = []meetings.Occurrence{occ}
}

func TestMeetingTilePushedInsideWindow(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid1",
		Title: "STANDUP",
		Start: now.Add(30 * time.Minute),
		End:   now.Add(45 * time.Minute),
	})

	c.reconcileTiles(now)

	names := pub.CustomNamesSnapshot()
	if len(names) != 1 || names[0] != "ember-meet" {
		t.Fatalf("expected one ember-meet push, got %v", names)
	}
	apps := pub.CustomAppsSnapshot()
	if len(apps) != 1 {
		t.Fatalf("expected one payload, got %d", len(apps))
	}
	wantText := "30M STANDUP"
	if got := apps[0]["text"]; got != wantText {
		t.Errorf("payload text = %q, want %q", got, wantText)
	}
}

func TestMeetingTileAbsentOutsideWindow(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid2",
		Title: "STANDUP",
		Start: now.Add(90 * time.Minute),
		End:   now.Add(105 * time.Minute),
	})

	c.reconcileTiles(now)

	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("outside window: want 0 CustomApp calls, got %d", got)
	}
}

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

	c.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("first reconcile: want 1 push, got %d", got)
	}
	text1 := pub.CustomAppsSnapshot()[0]["text"]
	if text1 != "30M STANDUP" {
		t.Errorf("first push text = %q, want %q", text1, "30M STANDUP")
	}

	now2 := now.Add(time.Minute)
	store.mu.Lock()
	store.lastFetchOK = now2
	store.mu.Unlock()
	c.reconcileTiles(now2)

	apps := pub.CustomAppsSnapshot()
	if len(apps) != 2 {
		t.Fatalf("second reconcile: want 2 pushes total, got %d", len(apps))
	}
	text2 := apps[1]["text"]
	if text2 != "29M STANDUP" {
		t.Errorf("second push text = %q, want %q", text2, "29M STANDUP")
	}
}

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

	c.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	nowPast := start.Add(time.Second)
	store.mu.Lock()
	store.upcoming = nil
	store.lastFetchOK = nowPast
	store.mu.Unlock()

	c.reconcileTiles(nowPast)
	cleared := pub.ClearedAppsSnapshot()
	if len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("past start: want ClearApp(ember-meet), got %v", cleared)
	}
	if _, tracked := c.tiles.pushed["ember-meet"]; tracked {
		t.Error("ember-meet should leave the ledger after clear")
	}

	c.reconcileTiles(nowPast.Add(time.Minute))
	if got := len(pub.ClearedAppsSnapshot()); got != 1 {
		t.Errorf("second reconcile: want still 1 clear, got %d", got)
	}
}

func TestMeetingTileClearsWhenStale(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c, pub, store := meetingCoordFixture(t, now, 60)

	stalePast := now.Add(meetingsStaleTTL + time.Minute)

	seedMeeting(store, meetings.Occurrence{
		UID:   "uid5",
		Title: "STANDUP",
		Start: now.Add(30 * time.Minute),
		End:   now.Add(45 * time.Minute),
	})
	c.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	meetingStart := stalePast.Add(30 * time.Minute)
	seedMeeting(store, meetings.Occurrence{
		UID:   "uid5",
		Title: "STANDUP",
		Start: meetingStart,
		End:   meetingStart.Add(30 * time.Minute),
	})
	c.reconcileTiles(stalePast)

	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("stale feed: want ClearApp(ember-meet), got %v", cleared)
	}
}

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

	c.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("setup: want 1 push, got %d", got)
	}

	cfg := *c.loadCfg()
	cfg.Meetings.Enabled = boolPtr(false)
	c.loadCfg = func() *Config { return &cfg }

	c.reconcileTiles(now)
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-meet" {
		t.Errorf("disabled: want ClearApp(ember-meet), got %v", cleared)
	}
}

func TestMeetingTileAdopted(t *testing.T) {
	pub := &recordingPublisher{loopApps: []string{
		"Time", "ember", "ember-weather", "ember-meet",
	}}
	cfg := defaultConfig()
	cfg.Meetings.applyDefaults()
	cfg.Meetings.Enabled = boolPtr(false)
	app := NewApp(cfg, pub, testLogger())
	c := app.coord

	if !c.adoptDeviceManagedApps() {
		t.Fatal("adopt should succeed when device loop is readable")
	}
	if _, tracked := c.tiles.pushed["ember-meet"]; !tracked {
		t.Fatal("ember-meet should be seeded in the ledger after adopt")
	}

	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	c.reconcileTiles(now)
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
