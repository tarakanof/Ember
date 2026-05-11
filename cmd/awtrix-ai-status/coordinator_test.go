package main

import (
	"context"
	"testing"
	"time"
)

func TestNewCoordinator_DefaultsFromConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

	if c.dwell != 3*time.Second {
		t.Errorf("dwell = %v, want 3s", c.dwell)
	}
	if c.ackTimeout != 30*time.Second {
		t.Errorf("ackTimeout = %v, want 30s", c.ackTimeout)
	}
	if cap(c.cmds) < 1 {
		t.Errorf("cmds channel must be buffered")
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
	if gotPtr != "a|b|s2" {
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
		sessionKey: "a|b|s2",
		priorState: "running",
		newState:   "waiting",
	})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotPtr := c.pointer
	gotLocked := c.locked
	c.muTest.RUnlock()
	if gotPtr != "a|b|s2" {
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
		sessionKey: "a|b|s1",
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

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a|b|w", priorState: "running", newState: "waiting"})
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
