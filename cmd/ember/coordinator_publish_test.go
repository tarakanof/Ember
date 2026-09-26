package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	payloads []map[string]any // every attempt's payload, failed ones included
}

func (p *attemptPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	p.mu.Lock()
	budget := time.Duration(-1)
	if dl, ok := ctx.Deadline(); ok {
		budget = time.Until(dl)
	}
	p.budgets = append(p.budgets, budget)
	p.payloads = append(p.payloads, payload)
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

func (p *attemptPublisher) payloadsSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.payloads))
	copy(out, p.payloads)
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

	c.tiles.push(coordTileWriter{c}, "ember-weather", map[string]any{"text": "20C"}, time.Now())

	if got := len(pub.budgetsSnapshot()); got != 2 {
		t.Errorf("CustomApp attempts = %d, want 2 (one retry after a transient failure)", got)
	}
	if _, tracked := c.tiles.pushed["ember-weather"]; !tracked {
		t.Error("tile not in the ledger after a retried-then-successful push; the tile would be re-pushed every tick")
	}
}

// A 5xx is the clock talking while it can't serve — it watchdog-resets and runs
// its HTTP server on the task that drives the panel. That is worth another
// attempt, unlike a 4xx verdict on the payload itself.
func TestPublishRetriesDeviceServerError(t *testing.T) {
	for _, status := range []int{500, 503, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			pub := &attemptPublisher{recordingPublisher: &recordingPublisher{},
				failures: 1, err: &awtrix.APIError{StatusCode: status}}
			c, snap := publishFixture(t, pub, nil)

			c.publish(snap)

			if got := len(pub.budgetsSnapshot()); got != 2 {
				t.Errorf("CustomApp attempts after %d = %d, want 2", status, got)
			}
		})
	}
}

// A 4xx that isn't 429 is the device's final answer on this payload.
func TestPublishDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{400, 404, 413, 422} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			pub := &attemptPublisher{recordingPublisher: &recordingPublisher{},
				failures: publishAttempts, err: &awtrix.APIError{StatusCode: status}}
			c, snap := publishFixture(t, pub, nil)

			c.publish(snap)

			if got := len(pub.budgetsSnapshot()); got != 1 {
				t.Errorf("CustomApp attempts after %d = %d, want 1", status, got)
			}
		})
	}
}

// On shutdown the retry must not fire: the coordinator's context is cancelled,
// and a second attempt would just wait out its own deadline against a device
// nobody is listening to any more.
func TestPublishDoesNotRetryAfterShutdown(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: context.Canceled}
	c, snap := publishFixture(t, pub, nil)
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	cancel()

	c.publish(snap)

	if got := len(pub.budgetsSnapshot()); got != 1 {
		t.Errorf("CustomApp attempts with a cancelled coordinator context = %d, want 1", got)
	}
}

// A configured awtrix.timeout_seconds below the per-attempt default is a
// deliberate "this device answers fast or not at all" and must still cap the
// attempt — the clamp is a floor on impatience, not a way to widen it.
func TestPublishAttemptHonoursSmallerConfiguredTimeout(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: context.DeadlineExceeded}
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.AWTRIX.TimeoutSeconds = 1
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)
	c.ctx = context.Background()
	snap := Snapshot{Now: time.Now(), Sessions: []render.Session{
		{Source: "mbp", Tool: "claude", Session: "a", State: "running", UpdatedAt: time.Now()},
	}}
	c.snapshot = func() Snapshot { return snap }

	c.publish(snap)

	for i, b := range pub.budgetsSnapshot() {
		if b > time.Second {
			t.Errorf("attempt %d budget = %v with awtrix.timeout_seconds=1, want <= 1s", i, b)
		}
	}
}

// The retry re-sends the same frame. A retry that rebuilt the payload could
// push a frame the coordinator never decided on.
func TestPublishRetrySendsTheSamePayload(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: 1, err: context.DeadlineExceeded}
	c, snap := publishFixture(t, pub, nil)

	c.publish(snap)

	sent := pub.payloadsSnapshot()
	if len(sent) != 2 {
		t.Fatalf("recorded payloads = %d, want 2", len(sent))
	}
	first, _ := json.Marshal(sent[0])
	second, _ := json.Marshal(sent[1])
	if !bytes.Equal(first, second) {
		t.Errorf("retry payload differs from the first attempt:\n first: %s\nsecond: %s", first, second)
	}
}

// A push that succeeds on its second attempt counts as one ok publish, so the
// retry counter is the only thing that still sees the link degrading.
func TestPublishRetryIsCounted(t *testing.T) {
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: 1, err: context.DeadlineExceeded}
	m := newMetrics()
	c, snap := publishFixture(t, pub, m)

	c.publish(snap)

	if got := m.publishRetries.Load(); got != 1 {
		t.Errorf("publish retry metric = %d, want 1", got)
	}
	if ok := m.publishTotalOK.Load(); ok != 1 {
		t.Errorf("publish ok metric = %d, want 1", ok)
	}
}

// A device rejection of a tile is final, exactly as for the main frame.
func TestTilePublishDoesNotRetryDeviceRejection(t *testing.T) {
	rejected := &awtrix.APIError{StatusCode: 422, Code: "validationFailed"}
	pub := &attemptPublisher{recordingPublisher: &recordingPublisher{}, failures: publishAttempts, err: rejected}
	c, _ := publishFixture(t, pub, nil)

	c.tiles.push(coordTileWriter{c}, "ember-weather", map[string]any{"text": "20C"}, time.Now())

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

	// The first tick once the window has passed renews the frame...
	window := renewalDedupWindow(cfg.Display.FrameLifetimeSeconds, cfg.Display.RotationDwellSeconds)
	clk.Advance(window)
	c.publish(snap)
	if got := len(pub.CustomAppsSnapshot()); got != 2 {
		t.Fatalf("publishes after the %v dedup window = %d, want 2", window, got)
	}

	// ...and it does so with a full retry budget still to spare, which is the
	// point of the margin: this worst-case tick lands a whole dwell late.
	spare := lifetime - (dwell + window)
	if budget := time.Duration(publishAttempts) * publishAttemptTimeout; spare < budget {
		t.Errorf("renewal landed with %v before eviction, want >= %v (one pushApp budget)", spare, budget)
	}
}

// The renewal margin is what a lossy link spends. The last tick before the
// window opens can land a full dwell early, so the wallclock slack before the
// device evicts the app is (margin - dwell) — and that has to cover at least
// one full pushApp budget, or a single dropped push still costs the app its
// slot. Swept across the validated frame_lifetime_seconds range [10,120] and
// the dwell values a config can produce.
func TestRenewalMarginCoversTheRetryBudget(t *testing.T) {
	retryBudget := time.Duration(publishAttempts) * publishAttemptTimeout
	for _, lifetime := range []int{10, 11, 20, 30, 45, 60, 90, 120} {
		for _, dwell := range []int{1, 3, 6, 10, 20, 40} {
			if dwell >= lifetime {
				continue // a dwell longer than the lifetime is not a coherent config
			}
			window := renewalDedupWindow(lifetime, dwell)
			if window < time.Second {
				t.Errorf("lifetime=%ds dwell=%ds: window=%v, want >= 1s", lifetime, dwell, window)
			}
			if window == time.Second {
				continue // already re-pushing on every tick; nothing left to give
			}
			margin := time.Duration(lifetime)*time.Second - window
			if slack := margin - time.Duration(dwell)*time.Second; slack < retryBudget {
				t.Errorf("lifetime=%ds dwell=%ds: window=%v leaves %v of slack before eviction, want >= %v (one pushApp budget)",
					lifetime, dwell, window, slack, retryBudget)
			}
		}
	}
}

// At the defaults the renewal must get more than one shot — that "exactly one
// attempt" is what let a single dropped push evict the app — while still
// leaving a real dedup window (the defaults are not a degenerate config).
func TestDefaultRenewalMarginBuysMoreThanOneAttempt(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	lifetime, dwell := cfg.Display.FrameLifetimeSeconds, cfg.Display.RotationDwellSeconds
	window := renewalDedupWindow(lifetime, dwell)
	if window <= time.Duration(dwell)*time.Second {
		t.Errorf("default dedup window = %v with dwell %ds; the defaults should still dedupe identical frames", window, dwell)
	}
	slack := time.Duration(lifetime)*time.Second - window - time.Duration(dwell)*time.Second
	if attempts := int(slack/publishAttemptTimeout) + 1; attempts < 3 {
		t.Errorf("default lifetime=%ds dwell=%ds leaves %v of renewal slack = %d push attempts, want >= 3",
			lifetime, dwell, slack, attempts)
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

// TestCoord_DedupesIdenticalPublishes verifies that re-publishing the
// same payload within the dedup window (< lifetime) is skipped. Without
// this, every dwell tick re-POSTs /api/custom and the firmware restarts
// the blinkText phase mid-cycle, producing a visible animation stutter.
func TestCoord_DedupesIdenticalPublishes(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	// applyDefaults sets FrameLifetimeSeconds=30 and RotationDwellSeconds=3, so
	// the dedup window is renewalDedupWindow(30,3) = 20s. (A much shorter
	// lifetime no longer dedupes at all: the renewal margin needs room for a
	// full pushApp retry budget, and below that the window bottoms out at 1s.)
	dedupWindow := renewalDedupWindow(cfg.Display.FrameLifetimeSeconds, cfg.Display.RotationDwellSeconds)
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
	}}
	c.snapshot = func() Snapshot { return snap }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// Tick 1: initial publish.
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	// Tick 2 within dedup window (1s later): identical payload, should be skipped.
	clk.Advance(1 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != 1 {
		t.Errorf("publishes after dedup-window tick = %d, want 1 (identical payload should be skipped)", got)
	}

	// Tick 3 past the dedup window.
	clk.Advance(dedupWindow)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != 2 {
		t.Errorf("publishes after lifetime expiry = %d, want 2 (must refresh before app evicts)", got)
	}
}

// TestCoord_RepublishClearsDedupeAndRepushes covers the reboot-recovery path:
// pushed apps are RAM-only on awtrix-ng, so once the device restarts the
// coordinator's dedupe cache describes apps that no longer exist. A republish
// must drop that cache and push again on the spot, without waiting for the
// dedupe window (or the frame lifetime) to expire.
func TestCoord_RepublishClearsDedupeAndRepushes(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.ctx = context.Background()

	snap := Snapshot{Sessions: []Session{
		{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
	}}
	c.snapshot = func() Snapshot { return snap }

	c.publish(snap)
	if got := len(publisher.CustomAppsSnapshot()); got != 1 {
		t.Fatalf("publishes after first publish = %d, want 1", got)
	}
	// Identical payload inside the dedupe window: skipped, as designed.
	c.publish(snap)
	if got := len(publisher.CustomAppsSnapshot()); got != 1 {
		t.Fatalf("publishes after deduped publish = %d, want 1", got)
	}

	c.onRepublish()
	if got := len(publisher.CustomAppsSnapshot()); got != 2 {
		t.Fatalf("publishes after republish = %d, want 2 (dedupe must be cleared)", got)
	}
	if c.mainPushed.body == nil {
		t.Fatal("republish should have re-armed the dedupe cache from the fresh push")
	}
}

// TestCoord_RepublishRepushesStandaloneTiles guards the other half of the
// dedupe reset: the tile ledger must be dropped too,
// or a tile whose content hasn't changed would never come back after a reboot.
func TestCoord_RepublishRepushesStandaloneTiles(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RotateInApps = boolPtr(true)
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	c.ctx = context.Background()
	now := time.Now()

	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 21, FetchedAt: now}
	app.weather.have = true
	app.weather.mu.Unlock()

	c.reconcileTiles(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-weather" {
		t.Fatalf("expected one ember-weather push, got %v", names)
	}
	// Unchanged content inside the refresh window: no re-push.
	c.reconcileTiles(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("pushes after unchanged reconcile = %d, want 1", got)
	}

	c.onRepublish()
	var repushed bool
	for _, n := range pub.CustomNamesSnapshot()[1:] {
		if n == "ember-weather" {
			repushed = true
		}
	}
	if !repushed {
		t.Fatalf("weather tile not re-pushed after republish: %v", pub.CustomNamesSnapshot())
	}
}

// TestCoord_DedupePublishesAgainOnStateChange verifies that a payload
// change (state transition, lock acquisition, etc.) bypasses the dedup
// window so attention transitions land immediately.
func TestCoord_DedupePublishesAgainOnStateChange(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var stateMu sync.RWMutex
	state := "running"
	c.snapshot = func() Snapshot {
		stateMu.RLock()
		s := state
		stateMu.RUnlock()
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: s, UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	// Same instant, transition to waiting — payload changes (text=WAIT
	// appears) and the publish must NOT be skipped despite no clock advance.
	stateMu.Lock()
	state = "waiting"
	stateMu.Unlock()
	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != 2 {
		t.Errorf("publishes after state-change upsert = %d, want 2 (payload differs, dedup must not skip)", got)
	}
}
