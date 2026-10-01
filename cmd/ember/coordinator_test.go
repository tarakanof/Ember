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

	if got := c.ackTimeoutDur(); got != 30*time.Second {
		t.Errorf("ackTimeoutDur() = %v, want 30s", got)
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

type blockingPublisher struct {
	*recordingPublisher
	release chan struct{}
}

func (p *blockingPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	<-p.release
	return p.recordingPublisher.CustomApp(ctx, name, payload)
}

func TestCoord_Send_DoesNotBackPressureProducers(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	pub := &blockingPublisher{recordingPublisher: &recordingPublisher{}, release: make(chan struct{})}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	m := newMetrics()
	c := newCoordinator(cfg, nil, pub, clk, nil, m)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "running", newState: "running"})
	t.Cleanup(func() { close(pub.release) })

	const flood = 500
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for i := 0; i < flood; i++ {
			c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "running", newState: "running"})
		}
		done <- time.Since(start)
	}()

	select {
	case d := <-done:
		if d > 2*time.Second {
			t.Errorf("flooding %d Send calls took %v; producer handlers are being back-pressured", flood, d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send flood blocked > 3s while device wedged — coordinator back-pressures producer HTTP handlers")
	}

	if got := m.commandsDropped.Load(); got == 0 {
		t.Errorf("commands_dropped counter = 0, want > 0 (overflow past the 64-slot buffer must be dropped and counted)")
	}
}

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
			{Source: "mbp", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()},
			{Source: "studio", Tool: "b", Session: "s2", State: "running", UpdatedAt: clk.Now()},
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
	c.stateMu.RLock()
	gotPtr := c.pointer
	c.stateMu.RUnlock()
	if gotPtr != "studio/b/s2" {
		t.Errorf("pointer after 2 ticks = %q, want studio/b/s2 (wrap-from-s1)", gotPtr)
	}
}

func TestCoord_Preempt_OnWaitingTransition(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)

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

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	beforePtr := c.pointer
	c.stateMu.RUnlock()
	if beforePtr != "a/b/s1" {
		t.Fatalf("setup: pointer before preempt = %q, want a/b/s1 (so a real jump can happen)", beforePtr)
	}

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

	c.stateMu.RLock()
	gotPtr := c.pointer
	gotLocked := c.locked
	c.stateMu.RUnlock()
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

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	c.Send(coordCmd{
		kind:       cmdUpsert,
		sessionKey: "a/b/s1",
		priorState: "waiting",
		newState:   "waiting",
	})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotLocked := c.locked
	c.stateMu.RUnlock()
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

	c.stateMu.RLock()
	if !c.locked {
		c.stateMu.RUnlock()
		t.Fatal("expected locked")
	}
	c.stateMu.RUnlock()

	stateMu.Lock()
	state = "running"
	stateMu.Unlock()
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotLocked := c.locked
	c.stateMu.RUnlock()
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

	sessMu.Lock()
	sessions = nil
	sessMu.Unlock()
	c.Send(coordCmd{kind: cmdDelete, sessionKey: "a/b/s"})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotLocked := c.locked
	gotPtr := c.pointer
	c.stateMu.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after delete, want false")
	}
	if gotPtr != "" {
		t.Errorf("pointer = %q, want empty after delete", gotPtr)
	}
}

func TestCoord_ClearPublishes(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot { return Snapshot{} }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdClear})
	time.Sleep(50 * time.Millisecond)

	if len(publisher.CustomAppsSnapshot()) == 0 {
		t.Errorf("onClear published 0 frames; want a publish like onUpsert/onDelete")
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
	live.Store(false)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotLocked := c.locked
	c.stateMu.RUnlock()
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

	for i := 0; i < 3; i++ {
		c.Send(coordCmd{kind: cmdTick})
		time.Sleep(20 * time.Millisecond)
	}

	c.stateMu.RLock()
	gotPtr := c.pointer
	c.stateMu.RUnlock()
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

	c.stateMu.RLock()
	if !c.locked {
		c.stateMu.RUnlock()
		t.Fatal("expected locked after waiting transition")
	}
	c.stateMu.RUnlock()

	clk.Advance(31 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotLocked := c.locked
	c.stateMu.RUnlock()
	if gotLocked {
		t.Errorf("locked = true after 31s, want false (ack timeout %v)", c.ackTimeoutDur())
	}
}

func TestCoord_IdleCountdown_Off(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.IdleRestoreSeconds = 60
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot { return Snapshot{} }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	beforeExpiry := len(publisher.CustomAppsSnapshot())
	if beforeExpiry != 1 {
		t.Fatalf("publishes after first idle tick = %d, want 1", beforeExpiry)
	}

	clk.Advance(61 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	if got := len(publisher.CustomAppsSnapshot()); got != beforeExpiry {
		t.Errorf("publishes after expiry tick = %d, want unchanged at %d (no publish)", got, beforeExpiry)
	}
}

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

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	idleCount := len(publisher.CustomAppsSnapshot())
	clk.Advance(61 * time.Second)
	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)
	if len(publisher.CustomAppsSnapshot()) != idleCount {
		t.Fatalf("countdown not yet idle-off")
	}

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
	reach := 0
	for _, raw := range last["draw"].([]any) {
		op := raw.([]any)
		if r := op[1].(int) + op[3].(int); r > reach {
			reach = r
		}
	}
	if reach != 32 {
		t.Errorf("active frame reaches col %d, want the full 32 (full rotation render)", reach)
	}
}

func TestCoord_Interleave_SourceAndToolCards(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, nil, publisher, clk, nil, nil)
	c.snapshot = func() Snapshot {
		return Snapshot{Sessions: []Session{
			{Source: "a", Tool: "b", Session: "s1", State: "running", Activity: "Bash: x", UpdatedAt: clk.Now()},
		}}
	}
	if got := render.CardsForSession(c.snapshot().Sessions[0], nil); got != 2 {
		t.Fatalf("CardsForSession = %d, want 2", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	readCursor := func() int {
		c.stateMu.RLock()
		defer c.stateMu.RUnlock()
		return c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	tick()
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 1, cardCursor = %d, want 0", got)
	}
	tick()
	if got := readCursor(); got != 1 {
		t.Fatalf("after tick 2, cardCursor = %d, want 1", got)
	}
	tick()
	if got := readCursor(); got != 0 {
		t.Fatalf("after tick 3, cardCursor = %d, want 0 (wrap)", got)
	}
}

func TestCoord_Interleave_TwoSessionsOrder(t *testing.T) {
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

	read := func() (string, int) {
		c.stateMu.RLock()
		defer c.stateMu.RUnlock()
		return c.pointer, c.cardCursor
	}
	tick := func() { c.Send(coordCmd{kind: cmdTick}); time.Sleep(50 * time.Millisecond) }

	type stop struct {
		ptr  string
		card int
	}
	want := []stop{{"a/b/s1", 0}, {"a/b/s2", 0}, {"a/b/s1", 0}, {"a/b/s2", 0}}
	for i, w := range want {
		tick()
		p, cu := read()
		if p != w.ptr || cu != w.card {
			t.Fatalf("after tick %d: (%q, %d), want (%q, %d)", i+1, p, cu, w.ptr, w.card)
		}
	}
}

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

	c.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	clk.Advance(30 * time.Second)
	snapMu.Lock()
	sessions = []Session{{Source: "a", Tool: "b", Session: "s1", State: "running", UpdatedAt: clk.Now()}}
	snapMu.Unlock()
	c.Send(coordCmd{kind: cmdUpsert, sessionKey: "a/b/s1", priorState: "", newState: "running"})
	time.Sleep(50 * time.Millisecond)

	c.stateMu.RLock()
	gotIdleSince := c.idleSince
	c.stateMu.RUnlock()
	if !gotIdleSince.IsZero() {
		t.Errorf("idleSince = %v, want zero (cancelled by active session)", gotIdleSince)
	}
}
