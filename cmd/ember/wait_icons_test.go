package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestEnsureWaitIconsUploadsTheMissingLogosAndArmsThem(t *testing.T) {
	pub := &recordingPublisher{icons: []string{render.WaitIconName("codex") + ".gif"}}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.ensureNativeIcons(context.Background())
	got := pub.PutIconNamesSnapshot()
	slices.Sort(got)
	want := []string{render.WaitIconName("claude") + ".gif", render.WaitIconName("t3") + ".gif"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
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
	if p["icon"] != render.WaitIconName("claude") || p["text"] != "learn-language" {
		t.Fatalf("payload = %v", p)
	}
}

func TestClockRebootReprovisionsTheWaitIcons(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.waitIcons.Store(true)
	app.clockRebooted("clock_reboot")
	app.clockJobs.Wait()
	if got := pub.PutIconNamesSnapshot(); !slices.Contains(got, render.WaitIconName("claude")+".gif") {
		t.Fatalf("uploads after a reboot = %v, want the wait icons checked and restored", got)
	}
}

func TestProjectCardStaysUntilItsNameHasScrolledOnce(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	clk := newFakeClock()
	c := newCoordinator(cfg, nil, &recordingPublisher{}, clk, nil, nil)
	long := "a-really-long-project-folder-nam"
	c.snapshot = func() Snapshot {
		return Snapshot{Now: clk.Now(), Sessions: []Session{
			{Source: "m4", Tool: "claude", Session: "s", State: "waiting", Project: long, UpdatedAt: clk.Now(), StateSince: clk.Now()},
		}}
	}
	c.onTick()
	if c.cardCursor != 0 {
		t.Fatalf("cursor = %d, want the project card first", c.cardCursor)
	}
	clk.Advance(3 * time.Second)
	c.onTick()
	if c.cardCursor != 0 {
		t.Fatal("the project card moved on before its name had scrolled through")
	}
	clk.Advance(10 * time.Second)
	c.onTick()
	if c.cardCursor != 1 {
		t.Fatalf("cursor = %d, want the next card once the scroll is done", c.cardCursor)
	}
}

func TestFailedReuploadDisarmsTheWaitIcons(t *testing.T) {
	pub := &recordingPublisher{putIconErr: errTest}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.waitIcons.Store(true)
	app.ensureNativeIcons(context.Background())
	if app.waitIcons.Load() {
		t.Fatal("a clock that lost the icons and refused the upload must fall back to the drawn icon")
	}
}

func TestProvisioningDeletesStaleWaitIcons(t *testing.T) {
	current := render.WaitIconName("claude") + ".gif"
	pub := &recordingPublisher{icons: append(waitIconFiles(), "ember_wait_claude_00000000.gif", "ember_wait_codex.gif", "29802.gif")}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.ensureNativeIcons(context.Background())
	got := pub.DeletedIconsSnapshot()
	slices.Sort(got)
	if want := []string{"ember_wait_claude_00000000.gif", "ember_wait_codex.gif"}; !slices.Equal(got, want) {
		t.Fatalf("deleted %v, want only the stale wait icons (kept %s)", got, current)
	}
}
