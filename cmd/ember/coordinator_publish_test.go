package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/render"
)

// attemptPublisher records one entry per CustomApp attempt (the deadline the
// caller granted it) and fails the first `failures` of them. Every other
// Publisher method delegates to the embedded recorder.
type attemptPublisher struct {
	*recordingPublisher
	mu       sync.Mutex
	failures int   // remaining attempts to fail
	err      error // what a failing attempt returns
	budgets  []time.Duration
}

func (p *attemptPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	p.mu.Lock()
	budget := time.Duration(-1)
	if dl, ok := ctx.Deadline(); ok {
		budget = time.Until(dl)
	}
	p.budgets = append(p.budgets, budget)
	fail := p.failures > 0
	if fail {
		p.failures--
	}
	p.mu.Unlock()
	if fail {
		return p.err
	}
	return p.recordingPublisher.CustomApp(ctx, name, payload)
}

func (p *attemptPublisher) budgetsSnapshot() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]time.Duration, len(p.budgets))
	copy(out, p.budgets)
	return out
}

// publishFixture wires a coordinator around pub with one running session.
func publishFixture(t *testing.T, pub Publisher, m *metrics) (*coordinator, Snapshot) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), m)
	c.ctx = context.Background()
	snap := Snapshot{Now: time.Now(), Sessions: []render.Session{
		{Source: "mbp", Tool: "claude", Session: "a", State: "running", UpdatedAt: time.Now()},
	}}
	c.snapshot = func() Snapshot { return snap }
	return c, snap
}

// A frame lost in flight (the device never answered before the deadline) must
// be retried inside the same tick: the device evicts a pushed app on its own
// lifetime, so waiting a whole dwell for the next attempt is what lets a lossy
// link drop the app out of the rotation entirely.
func TestPublishRetriesTransientDeviceFailure(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: 1, err: context.DeadlineExceeded}
	m := newMetrics()
	c, snap := publishFixture(t, pub, m)

	c.publish(snap)

	if got := len(pub.budgetsSnapshot()); got != 2 {
		t.Errorf("CustomApp attempts = %d, want 2 (one retry after a transient failure)", got)
	}
	if got := len(pub.CustomAppsSnapshot()); got != 1 {
		t.Errorf("frames landed on device = %d, want 1", got)
	}
	if ok, fail := m.publishTotalOK.Load(), m.publishTotalFail.Load(); ok != 1 || fail != 0 {
		t.Errorf("publish metrics ok=%d fail=%d, want ok=1 fail=0 (a retried-then-successful push is one successful publish)", ok, fail)
	}
}

// Each attempt gets its own bounded deadline. Without this the coordinator
// goroutine — which owns every device write — stalls for the full
// awtrix.timeout_seconds on a black-holed device, and the ticks it misses
// become dropped state-change commands.
func TestPublishBoundsEachAttemptDeadline(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: context.DeadlineExceeded}
	m := newMetrics()
	c, snap := publishFixture(t, pub, m)

	c.publish(snap)

	budgets := pub.budgetsSnapshot()
	if len(budgets) != publishAttempts {
		t.Fatalf("CustomApp attempts = %d, want %d", len(budgets), publishAttempts)
	}
	for i, b := range budgets {
		if b <= 0 {
			t.Errorf("attempt %d had no deadline; want one bounded by %v", i, publishAttemptTimeout)
			continue
		}
		if b > publishAttemptTimeout {
			t.Errorf("attempt %d deadline budget = %v, want <= %v", i, b, publishAttemptTimeout)
		}
	}
	if fail := m.publishTotalFail.Load(); fail != 1 {
		t.Errorf("publish fail metric = %d, want 1 (all attempts of one frame are one failed publish)", fail)
	}
}

// A device that answered — with a rejection — is not a lost packet. Retrying
// a 422 just spends the coordinator's time on an answer that will not change.
func TestPublishDoesNotRetryDeviceRejection(t *testing.T) {
	rejected := &awtrix.APIError{StatusCode: 422, Code: "validationFailed", Field: "text"}
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: rejected}
	m := newMetrics()
	c, snap := publishFixture(t, pub, m)

	c.publish(snap)

	if got := len(pub.budgetsSnapshot()); got != 1 {
		t.Errorf("CustomApp attempts = %d, want 1 (a device rejection is final)", got)
	}
	if fail := m.publishTotalFail.Load(); fail != 1 {
		t.Errorf("publish fail metric = %d, want 1", fail)
	}
}

// The rotating tiles ride the same lossy link as the main frame and are
// evicted by the same device-side lifetime, so they retry the same way.
func TestTilePublishRetriesTransientDeviceFailure(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: 1, err: context.DeadlineExceeded}
	c, _ := publishFixture(t, pub, nil)

	var tracker *pushedUsageApp
	c.reconcileTile(time.Now(), "ember-weather", &tracker, true, func() map[string]any {
		return map[string]any{"text": "20C"}
	})

	if got := len(pub.budgetsSnapshot()); got != 2 {
		t.Errorf("CustomApp attempts = %d, want 2 (one retry after a transient failure)", got)
	}
	if tracker == nil {
		t.Error("tile tracker = nil after a retried-then-successful push; the tile would be re-pushed every tick")
	}
}

// A device rejection of a tile is final, exactly as for the main frame.
func TestTilePublishDoesNotRetryDeviceRejection(t *testing.T) {
	rejected := &awtrix.APIError{StatusCode: 422, Code: "validationFailed"}
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: rejected}
	c, _ := publishFixture(t, pub, nil)

	var tracker *pushedUsageApp
	c.reconcileTile(time.Now(), "ember-weather", &tracker, true, func() map[string]any {
		return map[string]any{"text": "20C"}
	})

	if got := len(pub.budgetsSnapshot()); got != 1 {
		t.Errorf("CustomApp attempts = %d, want 1 (a device rejection is final)", got)
	}
}

// An unchanged frame is renewed several dwell ticks before the device would
// evict it. The old margin (one dwell + 1s) gave a lossy link a single attempt
// at the renewal, so one dropped push took the app out of the clock's rotation
// until the frame changed.
func TestUnchangedFrameRenewsSeveralTicksBeforeEviction(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	lifetime := time.Duration(cfg.Display.FrameLifetimeSeconds) * time.Second
	dwell := time.Duration(cfg.Display.RotationDwellSeconds) * time.Second
	pub := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, clk, testLogger(), nil)
	c.ctx = context.Background()
	snap := Snapshot{Sessions: []render.Session{
		{Source: "mbp", Tool: "claude", Session: "a", State: "running", UpdatedAt: clk.Now()},
	}}
	c.snapshot = func() Snapshot { return snap }

	c.publish(snap)
	if got := len(pub.CustomAppsSnapshot()); got != 1 {
		t.Fatalf("publishes after first frame = %d, want 1", got)
	}

	// One dwell later the identical frame is still deduped — renewal must not
	// mean re-pushing the same bitmap on every tick.
	clk.Advance(dwell)
	c.publish(snap)
	if got := len(pub.CustomAppsSnapshot()); got != 1 {
		t.Errorf("publishes one dwell after the first = %d, want 1 (identical frame should dedupe)", got)
	}

	// By the time the device is three dwell ticks from evicting the app, the
	// renewal must already have been attempted — leaving room for retries.
	clk.Advance(lifetime - 3*dwell - dwell)
	c.publish(snap)
	if got := len(pub.CustomAppsSnapshot()); got != 2 {
		t.Errorf("publishes %v before eviction = %d, want 2 (renewal must start with ticks to spare)", 3*dwell, got)
	}
}

// The default lifetime has to be long enough that a handful of consecutive
// failed pushes cannot expire the app between renewals.
func TestDefaultFrameLifetimeLeavesRoomForRetries(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	lifetime := cfg.Display.FrameLifetimeSeconds
	dwell := cfg.Display.RotationDwellSeconds
	if lifetime < 10*dwell {
		t.Errorf("default frame_lifetime_seconds = %d with dwell %d; want >= %d so a lossy link gets ~10 attempts per renewal",
			lifetime, dwell, 10*dwell)
	}
}

var errTransientPush = errors.New("transient push failure")

// Guard: the retry must not swallow a genuine, sustained device outage — the
// publish still fails and is still counted.
func TestPublishReportsFailureAfterAllAttempts(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: errTransientPush}
	m := newMetrics()
	c, snap := publishFixture(t, pub, m)

	var gotErr error
	c.onPublishResult = func(_ Snapshot, err error) { gotErr = err }

	c.publish(snap)

	if !errors.Is(gotErr, errTransientPush) {
		t.Errorf("onPublishResult err = %v, want %v", gotErr, errTransientPush)
	}
	if fail := m.publishTotalFail.Load(); fail != 1 {
		t.Errorf("publish fail metric = %d, want 1", fail)
	}
}
