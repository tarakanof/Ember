package main

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestEnsureWaitIconsUploadsTheMissingLogosAndArmsThem(t *testing.T) {
	pub := &recordingPublisher{icons: []string{"ember_wait_codex.gif"}}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.ensureNativeIcons(context.Background())
	got := pub.PutIconNamesSnapshot()
	slices.Sort(got)
	if want := []string{"ember_wait_claude.gif", "ember_wait_t3.gif"}; !slices.Equal(got, want) {
		t.Fatalf("uploaded %v, want %v", got, want)
	}
	if !app.waitIcons.Load() {
		t.Fatal("the coordinator may use the icons once they are on the clock")
	}
}

func TestEnsureWaitIconsStaysOffWhenTheClockCannotList(t *testing.T) {
	pub := &recordingPublisher{iconsErr: errTest}
	app := NewApp(defaultConfig(), pub, testLogger())
	app.ensureNativeIcons(context.Background())
	if app.waitIcons.Load() {
		t.Fatal("icons armed without reaching the clock")
	}
}

func TestClockMoveDisarmsTheWaitIcons(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{iconsErr: errTest}, testLogger())
	ownIconJobs(t, app)
	app.waitIcons.Store(true)
	app.clockMoved("test")
	app.clockJobs.Wait()
	if app.waitIcons.Load() {
		t.Fatal("a new clock must not be assumed to hold the icons")
	}
}

func TestLockedWaitPushesTheBlinkingLogoOnceProvisioned(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	pub := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, pub, clk, nil, nil)
	c.waitIcons = func() bool { return true }
	c.snapshot = func() Snapshot {
		return Snapshot{Now: clk.Now(), Sessions: []Session{
			{Source: "m4", Tool: "claude", Session: "s", State: "waiting", Project: "learn-language", UpdatedAt: clk.Now(), StateSince: clk.Now()},
		}}
	}
	c.handle(coordCmd{kind: cmdUpsert, sessionKey: "m4/claude/s", priorState: "running", newState: "waiting"})
	apps := pub.CustomAppsSnapshot()
	if len(apps) == 0 {
		t.Fatal("nothing pushed")
	}
	p := apps[len(apps)-1]
	if p["icon"] != "ember_wait_claude" || p["text"] != "learn-language" {
		t.Fatalf("payload = %v", p)
	}
}
