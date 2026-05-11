package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// clock abstracts the wall clock so coordinator tests can drive timers
// deterministically. Production uses realClock; tests inject fakeClock.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// coordCmdKind is the discriminator for coordCmd.
type coordCmdKind int

const (
	cmdTick     coordCmdKind = iota // dwell timer fired; advance rotation if not locked.
	cmdUpsert                       // a session was upserted; may trigger preempt.
	cmdDelete                       // a session was deleted; may release lock.
	cmdClear                        // all sessions cleared.
	cmdShutdown                     // graceful stop.
)

// coordCmd is a single command sent on the buffered command channel.
type coordCmd struct {
	kind       coordCmdKind
	sessionKey string // for upsert/delete
	priorState string // for upsert: the state BEFORE this upsert (empty for new)
	newState   string // for upsert: the state AFTER this upsert
}

// coordinator owns the single goroutine that decides what AWTRIX
// payload to publish and when. All AWTRIX HTTP writes pass through it.
type coordinator struct {
	loadCfg   func() *Config // shape matches App.cfg.Load directly
	publisher Publisher
	clk       clock
	logger    *slog.Logger
	metrics   *metrics // may be nil in tests that don't care about counters

	cmds chan coordCmd

	dwell      time.Duration
	ackTimeout time.Duration

	// State owned by the goroutine. Tests read it via muTest below.
	pointer       string
	locked        bool
	lockedKey     string
	lockEnteredAt time.Time

	// snapshot is set by the App when it wires the coordinator in (Task 8).
	// In tests, the test sets it directly.
	snapshot func() Snapshot

	// onPublishResult, if non-nil, is called after every publish attempt
	// with the error (nil on success). Used by App to update lastPublish*.
	onPublishResult func(err error)

	// muTest exists so tests can safely read coordinator-owned state
	// without data-race detector warnings. Production code never touches it.
	muTest sync.RWMutex

	publishCount atomic.Int64
}

// newCoordinator constructs the coordinator. The caller is responsible
// for starting its goroutine via Run.
//
// loadCfg returns the current *Config — pass `a.cfg.Load` from the App
// (atomic.Pointer[Config]) so reloadable fields (refresh_seconds) take
// effect at the next tick. Tests pass nil to capture cfg by value.
//
// m may be nil (tests that don't need metric counters).
func newCoordinator(cfg Config, loadCfg func() *Config, publisher Publisher, clk clock, logger *slog.Logger, m *metrics) *coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	if loadCfg == nil {
		captured := cfg
		loadCfg = func() *Config { return &captured }
	}
	return &coordinator{
		loadCfg:    loadCfg,
		publisher:  publisher,
		clk:        clk,
		logger:     logger,
		metrics:    m,
		cmds:       make(chan coordCmd, 8),
		dwell:      time.Duration(cfg.Display.RotationDwellSeconds) * time.Second,
		ackTimeout: time.Duration(cfg.Display.AckTimeoutSeconds) * time.Second,
	}
}

// Send enqueues a command, dropping if the buffer is full. Drops are
// logged at debug; the next tick will catch the system up.
func (c *coordinator) Send(cmd coordCmd) {
	select {
	case c.cmds <- cmd:
	default:
		c.logger.Debug("coord: dropped command (buffer full)", "kind", cmd.kind)
	}
}

// Run is the goroutine entry point. Cancels cleanly on ctx.Done.
func (c *coordinator) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-c.cmds:
			c.handle(cmd)
		}
	}
}

func (c *coordinator) handle(cmd coordCmd) {
	switch cmd.kind {
	case cmdTick:
		c.onTick()
	case cmdUpsert:
		c.onUpsert(cmd.sessionKey, cmd.priorState, cmd.newState)
	case cmdDelete:
		c.onDelete(cmd.sessionKey)
	case cmdClear:
		c.onClear()
	case cmdShutdown:
		// no-op for now
	}
}

func (c *coordinator) onUpsert(key, prior, next string) {
	attention := next == "waiting" || next == "error"
	transition := prior != next
	if attention && transition {
		c.muTest.Lock()
		c.pointer = key
		c.locked = true
		c.lockedKey = key
		c.lockEnteredAt = c.clk.Now()
		c.muTest.Unlock()
	}
	if c.snapshot != nil {
		c.publish(c.snapshot())
	}
}

func (c *coordinator) onDelete(key string) {
	c.muTest.Lock()
	if c.locked && c.lockedKey == key {
		c.locked = false
		c.lockedKey = ""
	}
	if c.pointer == key {
		c.pointer = ""
	}
	c.muTest.Unlock()
	if c.snapshot != nil {
		c.publish(c.snapshot())
	}
}

func (c *coordinator) onClear() {
	c.muTest.Lock()
	c.pointer = ""
	c.locked = false
	c.lockedKey = ""
	c.muTest.Unlock()
}

func (c *coordinator) onTick() {
	// Ack-timeout release: if locked for longer than ackTimeout, release.
	c.muTest.Lock()
	if c.locked && c.clk.Now().Sub(c.lockEnteredAt) >= c.ackTimeout {
		c.logger.Info("coord ack timeout release", "key", c.lockedKey)
		c.locked = false
		c.lockedKey = ""
	}
	c.muTest.Unlock()

	if c.snapshot == nil {
		return
	}
	snap := c.snapshot()
	keys := sortedActiveKeys(snap)
	if len(keys) == 0 {
		c.muTest.Lock()
		c.pointer = ""
		c.muTest.Unlock()
		return
	}
	next := pickRotated(c.pointer, keys)
	c.muTest.Lock()
	c.pointer = next
	c.muTest.Unlock()
	c.publish(snap)
}

func (c *coordinator) publish(snap Snapshot) {
	cfg := c.loadCfg()
	lifetime := max(5, cfg.Display.RefreshSeconds+2)
	payload := RenderForCoord(snap, c.pointer, c.locked, lifetime)
	if payload == nil {
		return
	}
	err := c.publisher.CustomApp(context.Background(), cfg.AWTRIX.AppName, payload)
	if err != nil {
		c.logger.Warn("coord publish failed", "err", err)
		c.metrics.incPublishFail()
	} else {
		c.publishCount.Add(1)
		c.metrics.incPublishOK()
	}
	if c.onPublishResult != nil {
		c.onPublishResult(err)
	}
}
