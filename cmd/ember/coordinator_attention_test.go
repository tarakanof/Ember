package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestAttentionChimeOncePerLock verifies that PlayRTTTL is called exactly once
// per fresh attention lock, not on waiting↔error re-arms, and not at all when
// AttentionChime is false.
func TestAttentionChimeOncePerLock(t *testing.T) {
	t.Run("chime_on_fresh_lock_only", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.applyDefaults()
		cfg.Display.AttentionChime = true
		publisher := &recordingPublisher{}
		clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

		c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
		c.snapshot = func() Snapshot {
			return Snapshot{Sessions: []Session{
				{Source: "a", Tool: "b", Session: "s", State: "waiting", UpdatedAt: clk.Now()},
			}}
		}

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go c.Run(ctx)

		// 1) Fresh lock: running → waiting. Expect exactly 1 chime.
		c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
		time.Sleep(50 * time.Millisecond)

		if got := len(publisher.RTTTLsSnapshot()); got != 1 {
			t.Errorf("after fresh lock: PlayRTTTL calls = %d, want 1", got)
		}

		// 2) Same key, waiting → error (re-arm branch). Still exactly 1 total.
		c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "waiting", newState: "error"})
		time.Sleep(50 * time.Millisecond)

		if got := len(publisher.RTTTLsSnapshot()); got != 1 {
			t.Errorf("after waiting→error re-arm: PlayRTTTL calls = %d, want 1 (no extra chime)", got)
		}

		// 3) Drain (→ running), then a new fresh waiting transition: now 2 total.
		c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "error", newState: "running"})
		time.Sleep(50 * time.Millisecond)

		c.muTest.RLock()
		stillLocked := c.locked
		c.muTest.RUnlock()
		if stillLocked {
			t.Fatal("expected lock released after drain to running")
		}

		c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
		time.Sleep(50 * time.Millisecond)

		if got := len(publisher.RTTTLsSnapshot()); got != 2 {
			t.Errorf("after second fresh lock: PlayRTTTL calls = %d, want 2", got)
		}
	})

	t.Run("no_chime_when_disabled", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.applyDefaults()
		cfg.Display.AttentionChime = false
		publisher := &recordingPublisher{}
		clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

		c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
		c.snapshot = func() Snapshot {
			return Snapshot{Sessions: []Session{
				{Source: "a", Tool: "b", Session: "s", State: "waiting", UpdatedAt: clk.Now()},
			}}
		}

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go c.Run(ctx)

		c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s", priorState: "running", newState: "waiting"})
		time.Sleep(50 * time.Millisecond)

		if got := len(publisher.RTTTLsSnapshot()); got != 0 {
			t.Errorf("with AttentionChime=false: PlayRTTTL calls = %d, want 0", got)
		}
	})
}

// TestAckTimeoutReadLive verifies that swapping the config's AckTimeoutSeconds
// at runtime takes effect for the CURRENT lock without a restart. With the old
// captured-field approach, advancing 6s while the captured timeout is 30s
// would leave the lock held; with live reads, 6s >= the new 5s timeout releases it.
func TestAckTimeoutReadLive(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.AckTimeoutSeconds = 30
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

	// Back the coordinator with a swappable config pointer.
	var cfgPtr atomic.Pointer[Config]
	stored := cfg
	cfgPtr.Store(&stored)
	loadCfg := func() *Config { return cfgPtr.Load() }

	c := newCoordinator(cfg, loadCfg, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "w", State: "waiting", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	// Acquire the lock.
	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/w", priorState: "running", newState: "waiting"})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	if !c.locked {
		c.muTest.RUnlock()
		t.Fatal("expected lock acquired")
	}
	c.muTest.RUnlock()

	// Shorten the timeout to 5s at runtime (still within the current lock).
	newCfg := *cfgPtr.Load()
	newCfg.Display.AckTimeoutSeconds = 5
	cfgPtr.Store(&newCfg)

	// Advance 6s — past the NEW 5s timeout but well under the OLD 30s.
	clk.Advance(6 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.muTest.RLock()
	gotLocked := c.locked
	c.muTest.RUnlock()

	if gotLocked {
		t.Errorf("locked = true after 6s with live AckTimeoutSeconds=5; expected live config read to have released the lock")
	}
}
