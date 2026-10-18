package main

import (
	"context"
	"slices"
	"sync"
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
	app := NewApp(stubClockConfig(t), &recordingPublisher{iconsErr: errTest}, testLogger())
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
	app := NewApp(stubClockConfig(t), pub, testLogger())
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

func TestStaleWaitIconsStayWhileTheCurrentSetIsNotArmed(t *testing.T) {
	pub := &recordingPublisher{icons: []string{render.WaitIconName("claude") + ".gif", "ember_wait_codex.gif"}, putIconErr: errTest}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	app.ensureNativeIcons(context.Background())
	if got := pub.DeletedIconsSnapshot(); len(got) != 0 {
		t.Fatalf("deleted %v while the current wait icons failed to upload", got)
	}
}

type gatedIconPublisher struct {
	*recordingPublisher
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *gatedIconPublisher) PutIcon(ctx context.Context, name string, data []byte) error {
	p.once.Do(func() {
		close(p.entered)
		<-p.release
	})
	return p.recordingPublisher.PutIcon(ctx, name, data)
}

func TestAProvisioningJobRunningAtAClockMoveDoesNotRearmTheWaitIcons(t *testing.T) {
	pub := &gatedIconPublisher{recordingPublisher: &recordingPublisher{}, entered: make(chan struct{}), release: make(chan struct{})}
	app := NewApp(defaultConfig(), pub, testLogger())
	ownIconJobs(t, app)
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.ensureNativeIcons(context.Background())
	}()
	<-pub.entered
	app.disarmWaitIcons()
	close(pub.release)
	<-done
	if app.waitIcons.Load() {
		t.Fatal("a job started for the old clock armed the wait icons on the new one")
	}
	app.ensureNativeIcons(context.Background())
	if !app.waitIcons.Load() {
		t.Fatal("the next provisioning run must arm them")
	}
}

type movingIconClock struct {
	*recordingPublisher
	mu          sync.Mutex
	clocks      [2][]string
	cur         int
	puts        int
	switchAfter int
	deleted     [2][]string
}

func (c *movingIconClock) ListIcons(context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.clocks[c.cur]...), nil
}

func (c *movingIconClock) PutIcon(_ context.Context, name string, _ []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clocks[c.cur] = append(c.clocks[c.cur], name)
	if c.puts++; c.puts == c.switchAfter {
		c.cur = 1
	}
	return nil
}

func (c *movingIconClock) deleteLocked(name string) {
	c.deleted[c.cur] = append(c.deleted[c.cur], name)
	c.clocks[c.cur] = slices.DeleteFunc(c.clocks[c.cur], func(n string) bool { return n == name })
}

func (c *movingIconClock) DeleteIcons(_ context.Context, pick func(string) bool) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, name := range append([]string(nil), c.clocks[c.cur]...) {
		if pick(name) {
			c.deleteLocked(name)
			out = append(out, name)
		}
	}
	return out, nil
}

func TestStaleWaitIconCleanupUsesTheListingOfTheClockItDeletesFrom(t *testing.T) {
	current := waitIconFiles()
	clock := &movingIconClock{recordingPublisher: &recordingPublisher{}, switchAfter: 2}
	clock.clocks[0] = []string{current[0], "ember_wait_codex.gif"}
	clock.clocks[1] = append(append([]string(nil), current...), "ember_wait_t3.gif")
	app := NewApp(defaultConfig(), clock, testLogger())
	ownIconJobs(t, app)
	app.ensureNativeIcons(context.Background())
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if !slices.Equal(clock.deleted[1], []string{"ember_wait_t3.gif"}) {
		t.Fatalf("deleted on the new clock %v, want only its own stale ember_wait_t3.gif (old clock: %v)", clock.deleted[1], clock.deleted[0])
	}
}
