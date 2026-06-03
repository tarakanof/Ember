package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestNewCoordinator_DefaultsFromConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	if c.ackTimeout != 30*time.Second {
		t.Errorf("ackTimeout = %v, want 30s", c.ackTimeout)
	}
	if cap(c.cmds) < 8 {
		t.Errorf("cmds channel buffer = %d, want a comfortable margin (>=8) so producer bursts don't drop transitions", cap(c.cmds))
	}
	if cap(c.ticks) != 1 {
		t.Errorf("ticks channel buffer = %d, want 1 (coalesce stale ticks)", cap(c.ticks))
	}
	_, cancel := context.WithCancel(context.Background())
	cancel()
}

// fakeClock is already declared in ratelimit_test.go (same package).

func TestCoord_Tick_SingleSession_PublishesOnce(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
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

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	apps := publisher.CustomAppsSnapshot()
	if got := len(apps); got != 1 {
		t.Errorf("custom app publishes = %d, want 1", got)
	}
	if len(apps) > 0 {
		if _, ok := apps[0]["draw"]; !ok {
			t.Errorf("payload missing draw key: %#v", apps[0])
		}
	}
}

// TestCoord_Tick_NoActive_EmitsIdleFrame replaces G.1b's NoPublish
// expectation. With G.2 display hold, the coordinator emits a dimmed
// idle frame on the first all-idle tick (start of the idle countdown)
// instead of ceding the slot immediately. The slot only releases after
// IdleRestoreSeconds elapses (covered by TestCoord_IdleCountdown_Off
// in Task 6).
func TestCoord_Tick_NoActive_EmitsIdleFrame(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot { return Snapshot{} }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	apps := publisher.CustomAppsSnapshot()
	if got := len(apps); got != 1 {
		t.Fatalf("publishes = %d, want 1 (idle countdown dim frame)", got)
	}
	// No text means no attention; the idle frame is robot-only.
	if _, hasText := apps[0]["text"]; hasText {
		t.Errorf("idle frame has text key; want robot-only dim frame")
	}
}

func TestCoord_Tick_TwoSessions_AdvancesPointer(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
			{Source: "a", Tool: "b", Session: "s2", State: "running", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != 2 {
		t.Fatalf("custom app publishes = %d, want 2", got)
	}
	c.muTest.RLock()
	gotPtr := c.pointer
	c.muTest.RUnlock()
	if gotPtr != "a/b/s2" {
		t.Errorf("pointer after 2 ticks = %q, want a|b|s2 (wrap-from-s1)", gotPtr)
	}
}

func TestCoord_Preempt_OnWaitingTransition(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	// Both sessions start in running so sortedActiveKeys orders by
	// (running-priority, source, tool, session) — s1 sorts before s2.
	// A vanilla cmdTick will put the pointer on s1. The preempt must
	// JUMP it to s2 once s2 transitions into waiting. Pre-fix the test
	// pre-seeded s2 as waiting which already sorts ahead, so a passing
	// assertion didn't actually prove the jump.
	var mu sync.Mutex
	s2State := "running"
	c.snapshot = func() Snapshot {
		mu.Lock()
		defer mu.Unlock()
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
			{Source: "a", Tool: "b", Session: "s2", State: s2State, UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// Tick — pointer ends up on s1 (alphabetically first under same priority).
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	beforePtr := c.pointer
	c.muTest.RUnlock()
	if beforePtr != "a/b/s1" {
		t.Fatalf("setup: pointer before preempt = %q, want a/b/s1 (so a real jump can happen)", beforePtr)
	}

	// Now flip s2 to waiting in the snapshot AND send the preempt command.
	mu.Lock()
	s2State = "waiting"
	mu.Unlock()
	c.Send(coordCmd{
		kind:       cmdUpsert,
		sessionKey: "a/b/s2",
		priorState: "running",
		newState:   "waiting",
	})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotPtr := c.pointer
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotPtr != "a/b/s2" {
		t.Errorf("pointer after waiting transition = %q, want a/b/s2 (jump from s1)", gotPtr)
	}
	if !gotLocked {
		t.Errorf("locked = false, want true after waiting transition")
	}
}

func TestCoord_Preempt_NotOnReheartbeat(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "waiting", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick}) // initial: picks s1, but not locked.
	time.Sleep(50 * time.Millisecond)
	c.Send(coordCmd{
		kind:       cmdUpsert,
		sessionKey: "a/b/s1",
		priorState: "waiting", // already waiting → no transition.
		newState:   "waiting",
	})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotLocked {
		t.Errorf("locked = true, want false (no state transition)")
	}
}

func TestCoord_DrainReleasesLock(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var stateMu sync.Mutex
	state := "waiting"
	c.snapshot = func() Snapshot {
		stateMu.Lock()
		s := state
		stateMu.Unlock()
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s", State: s, UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	if !c.locked {
		c.muTest.RUnlock()
		t.Fatal("expected locked")
	}
	c.muTest.RUnlock()

	// Session drains naturally to running.
	stateMu.Lock()
	state = "running"
	stateMu.Unlock()
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after drain, want false")
	}
}

func TestCoord_DeleteWhileLocked_ReleasesLock(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var sessMu sync.Mutex
	sessions := []Session{
		{Source: "a", Tool: "b", Session: "s", State: "waiting", UpdatedAt: clk.Now()},
	}
	c.snapshot = func() Snapshot {
		sessMu.Lock()
		ss := sessions
		sessMu.Unlock()
		return Snapshot{Sessions: ss}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	// Remove it from the snapshot, then send cmdDelete.
	sessMu.Lock()
	sessions = nil
	sessMu.Unlock()
	c.Send(coordCmd{kind: cmdDelete, sessionKey: "a/b/s"})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	gotPtr := c.pointer
	c.muTest.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after delete, want false")
	}
	if gotPtr != "" {
		t.Errorf("pointer = %q, want empty after delete", gotPtr)
	}
}

func TestCoord_ReapReleasesLock(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var live atomic.Bool
	live.Store(true)
	c.snapshot = func() Snapshot {
		if !live.Load() {
			return Snapshot{}
		}
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s", State: "waiting", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)
	live.Store(false) // reaped
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after reap, want false")
	}
}

func TestCoord_PointerPinned_WhenLocked(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "waiting", UpdatedAt: clk.Now()},
			{Source: "a", Tool: "b", Session: "s2", State: "running", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	// Tick three times while locked. Pointer must stay on s1.
	for i := 0; i < 3; i++ {
		c.Send(coordCmd{kind: cmdTick})
		time.Sleep(20 * time.Millisecond)
	}

	c.muTest.RLock()
	gotPtr := c.pointer
	c.muTest.RUnlock()
	if gotPtr != "a/b/s1" {
		t.Errorf("pointer drifted while locked: got %q, want a|b|s1", gotPtr)
	}
}

func TestCoord_AckTimeout_ReleasesLock(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/w", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	if !c.locked {
		c.muTest.RUnlock()
		t.Fatal("expected locked after waiting transition")
	}
	c.muTest.RUnlock()

	// Advance fake clock past the ack timeout, then tick.
	clk.Advance(31 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after 31s, want false (ack timeout %v)", c.ackTimeout)
	}
}

// TestCoord_DedupesIdenticalPublishes verifies that re-publishing the
// same payload within the dedup window (< lifetime) is skipped. Without
// this, every dwell tick re-POSTs /api/custom and the firmware restarts
// the blinkText phase mid-cycle, producing a visible animation stutter.
func TestCoord_DedupesIdenticalPublishes(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	// Use a short explicit lifetime so the dedup window is predictable.
	// applyDefaults sets FrameLifetimeSeconds=30 and RotationDwellSeconds=3;
	// override lifetime to 7 so dedupWindow = (7-3-1)s = 3s.
	cfg.Display.FrameLifetimeSeconds = 7
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

	// Tick 3 past dedup window (dedupWindow=3s; advance 7s, well past it).
	clk.Advance(7 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != 2 {
		t.Errorf("publishes after lifetime expiry = %d, want 2 (must refresh before app evicts)", got)
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

// TestCoord_IdleCountdown_Off verifies that after IdleRestoreSeconds
// of all-idle ticks, the coordinator stops publishing entirely so the
// device's lifetime elapses and AWTRIX scheduler returns to natives.
func TestCoord_IdleCountdown_Off(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.IdleRestoreSeconds = 60 // shorter window for tests
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot { return Snapshot{} }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// First tick: starts the countdown, emits dim frame.
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	beforeExpiry := len(publisher.CustomAppsSnapshot())
	if beforeExpiry != 1 {
		t.Fatalf("publishes after first idle tick = %d, want 1", beforeExpiry)
	}

	// Advance past the countdown.
	clk.Advance(61 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != beforeExpiry {
		t.Errorf("publishes after expiry tick = %d, want unchanged at %d (no publish)", got, beforeExpiry)
	}
}

// TestCoord_NewSessionAfterIdleExpiry_ResumesPublish covers the
// crash-safety bookend: once the device has gone back to natives
// (we've stopped publishing), a fresh non-idle session must wake the
// display by publishing immediately on the upsert command, with the
// rich active-session frame.
func TestCoord_NewSessionAfterIdleExpiry_ResumesPublish(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.IdleRestoreSeconds = 60
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var snapMu sync.RWMutex
	var sessions []Session
	c.snapshot = func() Snapshot {
		snapMu.RLock()
		defer snapMu.RUnlock()
		out := Snapshot{Sessions: append([]Session(nil), sessions...)}
		return out
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// Idle countdown starts, then expires.
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	idleCount := len(publisher.CustomAppsSnapshot())
	clk.Advance(61 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	if len(publisher.CustomAppsSnapshot()) != idleCount {
		t.Fatalf("countdown not yet idle-off")
	}

	// New session arrives.
	snapMu.Lock()
	sessions = []Session{{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()}}
	snapMu.Unlock()
	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "", newState: "running"})
	time.Sleep(50 * time.Millisecond)

	apps := publisher.CustomAppsSnapshot()
	if len(apps) != idleCount+1 {
		t.Fatalf("publishes after wake = %d, want %d (one new active-session publish)", len(apps), idleCount+1)
	}
	last := apps[len(apps)-1]
	// Active frame must NOT be the dim-white robot — it should be a state-coloured render.
	db := last["draw"].([]any)[0].(map[string]any)["db"].([]any)
	if db[2] != 32 {
		t.Errorf("active frame width = %v, want 32 (full rotation render)", db[2])
	}
}

func TestCoord_Interleave_SingleRateSession(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	pct := 42
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", RateWindowPct: &pct, UpdatedAt: clk.Now()},
		}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	readCursor := func() int {
		c.muTest.RLock()
		defer c.muTest.RUnlock()
		return c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	tick() // pointer="" → s1, cursor 0 (xy)
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 1, cardCursor = %d, want 0", got)
	}
	tick() // same session, has rate → cursor 1 (rate)
	if got := readCursor(); got != 1 {
		t.Fatalf("after tick 2, cardCursor = %d, want 1", got)
	}
	tick() // cards exhausted → wrap session, cursor 0
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 3, cardCursor = %d, want 0 (wrap)", got)
	}
}

func TestCoord_Interleave_RunningSessionThreeCards(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	pct := 42
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", RateWindowPct: &pct, Activity: "Bash: x", UpdatedAt: clk.Now()},
		}}
	}
	// XY + rate + tool = 3 cards.
	if got := render.CardsForSession(c.snapshot().Sessions[0]); got != 3 {
		t.Fatalf("cardsForSession = %d, want 3", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	readCursor := func() int {
		c.muTest.RLock()
		defer c.muTest.RUnlock()
		return c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	tick() // → s1, cursor 0 (xy)
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 1, cardCursor = %d, want 0", got)
	}
	tick() // cursor 1 (rate)
	if got := readCursor(); got != 1 {
		t.Fatalf("after tick 2, cardCursor = %d, want 1", got)
	}
	tick() // cursor 2 (tool)
	if got := readCursor(); got != 2 {
		t.Fatalf("after tick 3, cardCursor = %d, want 2", got)
	}
	tick() // cards exhausted → wrap, cursor 0
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 4, cardCursor = %d, want 0 (wrap)", got)
	}
}

func TestCoord_Interleave_RunningSessionFourCards(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	pct := 42
	ctxPct := 60
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", RateWindowPct: &pct, ContextNumber: true, ContextPct: &ctxPct, Activity: "Bash: x", UpdatedAt: clk.Now()},
		}}
	}
	// XY + rate + ctx + tool = 4 cards.
	if got := render.CardsForSession(c.snapshot().Sessions[0]); got != 4 {
		t.Fatalf("cardsForSession = %d, want 4", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	readCursor := func() int {
		c.muTest.RLock()
		defer c.muTest.RUnlock()
		return c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	for i, want := range []int{0, 1, 2, 3, 0} { // advance through all 4, then wrap
		tick()
		if got := readCursor(); got != want {
			t.Fatalf("after tick %d, cardCursor = %d, want %d", i+1, got, want)
		}
	}
}

func TestCoord_Interleave_TwoSessionsOrder(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	pct := 80
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", RateWindowPct: &pct, UpdatedAt: clk.Now()},
			{Source: "a", Tool: "b", Session: "s2", State: "running", UpdatedAt: clk.Now()},
		}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	read := func() (string, int) {
		c.muTest.RLock()
		defer c.muTest.RUnlock()
		return c.pointer, c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	type stop struct {
		ptr  string
		card int
	}
	want := []stop{{"a/b/s1", 0}, {"a/b/s1", 1}, {"a/b/s2", 0}, {"a/b/s1", 0}}
	for i, w := range want {
		tick()
		p, cu := read()
		if p != w.ptr || cu != w.card {
			t.Fatalf("after tick %d: (%q, %d), want (%q, %d)", i+1, p, cu, w.ptr, w.card)
		}
	}
}

// TestCoord_NewSessionMidCountdown_CancelsIdleTimer ensures that if a
// session arrives partway through the idle countdown, the dim-frame
// pathway is abandoned cleanly and the idleSince timestamp resets.
// Without this, a session that arrives at t=900s (with 1200s window)
// could be cut off 300s later instead of getting its full lifetime.
func TestCoord_NewSessionMidCountdown_CancelsIdleTimer(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.IdleRestoreSeconds = 60
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	var snapMu sync.RWMutex
	var sessions []Session
	c.snapshot = func() Snapshot {
		snapMu.RLock()
		defer snapMu.RUnlock()
		return Snapshot{Sessions: append([]Session(nil), sessions...)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// Start the idle countdown (no sessions).
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	// Halfway through the window, a session shows up.
	clk.Advance(30 * time.Second)
	snapMu.Lock()
	sessions = []Session{{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()}}
	snapMu.Unlock()
	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "", newState: "running"})
	time.Sleep(50 * time.Millisecond)

	// idleSince must be reset (zero) after the active publish.
	c.muTest.RLock()
	gotIdleSince := c.idleSince
	c.muTest.RUnlock()
	if !gotIdleSince.IsZero() {
		t.Errorf("idleSince = %v, want zero (cancelled by active session)", gotIdleSince)
	}
}
