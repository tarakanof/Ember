package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestCoord_Tick_NoActive_NoPublish(t *testing.T) {
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

	if got := len(publisher.CustomAppsSnapshot()); got != 0 {
		t.Errorf("custom app publishes = %d, want 0 (idle ⇒ cede slot)", got)
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
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
			{Source: "a", Tool: "b", Session: "s2", State: "waiting", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

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
		t.Errorf("pointer after waiting transition = %q, want a|b|s2", gotPtr)
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
