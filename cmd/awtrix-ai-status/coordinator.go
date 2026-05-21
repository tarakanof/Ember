package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
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

	// State-change commands (upsert/delete/clear/shutdown). Wide buffer
	// so a producer burst never drops an attention transition.
	cmds chan coordCmd
	// Ticks: 1-slot drop-on-full channel. Stale ticks carry no info.
	ticks chan struct{}

	ackTimeout time.Duration

	// State owned by the goroutine. Tests read it via muTest below.
	pointer       string
	cardCursor    int
	locked        bool
	lockedKey     string
	lockEnteredAt time.Time
	// lockReleaseTimer is a wallclock-based safety net that fires a tick
	// after ackTimeout, guaranteeing release even if dwell happens to be
	// configured larger than ackTimeout (the tick-driven check in onTick
	// is otherwise the only release path on a sleepy rotation cadence).
	// Tests still drive release via fakeClock + Send(cmdTick); the timer
	// uses real wallclock and so does nothing in those test setups —
	// behaviour stays test-friendly.
	lockReleaseTimer *time.Timer

	// idleSince tracks when the most recent transition to "no active
	// sessions" happened. Zero value means "currently have active
	// sessions" (the normal case). Used by publish() to decide between
	// active rendering, dimmed-idle rendering, and stopping publishing
	// once the countdown elapses.
	idleSince time.Time

	// snapshot is set by the App when it wires the coordinator in.
	// In tests, the test sets it directly.
	snapshot func() Snapshot

	// onPublishResult, if non-nil, is called after every publish attempt
	// with the snapshot we tried to render and the error (nil on success).
	// Used by App to update lastPublish* AND lastPublished (the legacy
	// Render metadata the admin endpoints expose).
	onPublishResult func(snap Snapshot, err error)

	// ctx is the Run context, used by publish() so an in-flight HTTP
	// publish cancels on shutdown rather than waiting for HTTP timeout.
	// Set on first Run() entry; before that, publish() falls back to
	// context.Background() (only happens in pathological test setups
	// that call publish before Run).
	ctx context.Context

	// muTest exists so tests can safely read coordinator-owned state
	// without data-race detector warnings. Production code never touches it.
	muTest sync.RWMutex

	publishCount atomic.Int64

	// lastPayloadBytes + lastPublishedAt dedupe identical re-publishes
	// within a window shorter than the AWTRIX app lifetime. Every
	// re-POST to /api/custom resets the firmware app's render state,
	// which restarts the blinkText phase mid-cycle as a visible
	// stutter; skipping no-op refreshes keeps the animation steady.
	// Only success updates these fields so failed publishes still
	// retry on the next tick.
	lastPayloadBytes []byte
	lastPublishedAt  time.Time
}

type coordIdleMode int

const (
	idleModeActive coordIdleMode = iota
	idleModeDimmed
	idleModeOff
)

// idleStateLocked decides which rendering branch publish should take.
// Caller MUST hold muTest. Returns the mode; mutates c.idleSince as a
// side effect (zero when active, set to now on first all-idle call).
func (c *coordinator) idleStateLocked(activeCount int, now time.Time, idleRestore time.Duration) coordIdleMode {
	if activeCount > 0 {
		c.idleSince = time.Time{}
		return idleModeActive
	}
	if c.idleSince.IsZero() {
		c.idleSince = now
	}
	if now.Sub(c.idleSince) >= idleRestore {
		return idleModeOff
	}
	return idleModeDimmed
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
		loadCfg:   loadCfg,
		publisher: publisher,
		clk:       clk,
		logger:    logger,
		metrics:   m,
		// State-change commands get a generous buffer so a burst of
		// producer activity never drops an upsert/delete/clear: those
		// carry the only signal of an attention transition. Ticks have
		// their own narrow drop-on-full channel since stale ticks add
		// no information (the next tick catches up).
		cmds:       make(chan coordCmd, 64),
		ticks:      make(chan struct{}, 1),
		ackTimeout: time.Duration(cfg.Display.AckTimeoutSeconds) * time.Second,
	}
}

// Send enqueues a command. Stale ticks (cmdTick) are dropped when their
// 1-slot channel is full — the next tick will pick up wherever the
// snapshot has landed. State-change commands (upsert/delete/clear) go
// to a wide buffer (64 slots); if THAT fills, we log at warn rather
// than drop, because losing an attention transition would defeat the
// preempt machinery. On a wedged channel, the runtime panic from a
// full-buffer send is preferable to silent loss.
func (c *coordinator) Send(cmd coordCmd) {
	if cmd.kind == cmdTick {
		select {
		case c.ticks <- struct{}{}:
		default:
			// Stale tick is fine to drop.
		}
		return
	}
	select {
	case c.cmds <- cmd:
	default:
		c.logger.Warn("coord: cmd channel full — blocking briefly", "kind", cmd.kind, "session_key", cmd.sessionKey)
		c.cmds <- cmd
	}
}

// Run is the goroutine entry point. Cancels cleanly on ctx.Done.
// The ctx is threaded into publish() so an in-flight HTTP publish
// cancels on shutdown rather than blocking on its full timeout.
// State-change commands win against ticks via channel ordering (Go's
// select is random when both are ready, so we drain cmds first
// opportunistically — preempt latency wins over rotation jitter).
func (c *coordinator) Run(ctx context.Context) {
	c.ctx = ctx
	for {
		// Opportunistic drain: if cmds has work, prefer it.
		select {
		case <-ctx.Done():
			return
		case cmd := <-c.cmds:
			c.handle(cmd)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case cmd := <-c.cmds:
			c.handle(cmd)
		case <-c.ticks:
			c.handle(coordCmd{kind: cmdTick})
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
	priorWasAttention := prior == "waiting" || prior == "error"
	transition := prior != next

	c.muTest.Lock()
	switch {
	case attention && transition && !priorWasAttention:
		// Fresh attention transition from a non-attention state.
		c.pointer = key
		c.cardCursor = 0
		c.locked = true
		c.lockedKey = key
		c.lockEnteredAt = c.clk.Now()
		c.armLockTimerLocked()
	case attention && transition && priorWasAttention && c.locked && c.lockedKey == key:
		// Same session shifting between waiting and error (e.g.,
		// waiting → error during an approval prompt that then failed).
		// Reset the ack timer so the new attention class gets its own
		// 30s window — but DON'T re-target the pointer.
		c.lockEnteredAt = c.clk.Now()
		c.armLockTimerLocked()
	case !attention && c.locked && c.lockedKey == key:
		// Drain: the locked session moved out of attention state. Release
		// the lock immediately rather than waiting for the next dwell tick.
		c.logger.Info("coord lock released", "key", c.lockedKey, "reason", "drain")
		c.locked = false
		c.lockedKey = ""
		c.disarmLockTimerLocked()
	}
	c.muTest.Unlock()

	if c.snapshot != nil {
		c.publish(c.snapshot())
	}
}

func (c *coordinator) onDelete(key string) {
	c.muTest.Lock()
	if c.locked && c.lockedKey == key {
		c.locked = false
		c.lockedKey = ""
		c.disarmLockTimerLocked()
	}
	if c.pointer == key {
		c.pointer = ""
		c.cardCursor = 0
	}
	c.muTest.Unlock()
	if c.snapshot != nil {
		c.publish(c.snapshot())
	}
}

func (c *coordinator) onClear() {
	c.muTest.Lock()
	c.pointer = ""
	c.cardCursor = 0
	c.locked = false
	c.lockedKey = ""
	c.disarmLockTimerLocked()
	c.muTest.Unlock()
}

// armLockTimerLocked installs (or replaces) the wallclock safety-net
// timer that fires a cmdTick after ackTimeout. Caller must hold muTest.
func (c *coordinator) armLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
	}
	c.lockReleaseTimer = time.AfterFunc(c.ackTimeout, func() {
		c.Send(coordCmd{kind: cmdTick})
	})
}

// disarmLockTimerLocked stops the safety-net timer. Caller must hold muTest.
func (c *coordinator) disarmLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
		c.lockReleaseTimer = nil
	}
}

func (c *coordinator) onTick() {
	if c.snapshot == nil {
		return
	}
	snap := c.snapshot()
	keys := sortedActiveKeys(snap)

	// Evaluate all lock-release conditions against the current snapshot:
	// ack timeout, drain (locked session moved out of attention state),
	// reap (locked key no longer in active set).
	c.muTest.Lock()
	if c.locked {
		releaseReason := ""
		if !slices.Contains(keys, c.lockedKey) {
			releaseReason = "reap"
		} else {
			// Drain: locked session moved out of attention state.
			for _, s := range snap.Sessions {
				if sessionKey(s) == c.lockedKey {
					if s.State != "waiting" && s.State != "error" {
						releaseReason = "drain"
					}
					break
				}
			}
		}
		if releaseReason == "" && c.clk.Now().Sub(c.lockEnteredAt) >= c.ackTimeout {
			releaseReason = "ack_timeout"
		}
		if releaseReason != "" {
			c.logger.Info("coord lock released", "key", c.lockedKey, "reason", releaseReason)
			c.locked = false
			c.lockedKey = ""
			c.disarmLockTimerLocked()
		}
	}
	c.muTest.Unlock()

	c.muTest.Lock()
	switch {
	case len(keys) == 0:
		c.pointer = ""
		c.cardCursor = 0
	case c.locked:
		// Locked: hold the target; cards never cycle during attention.
		c.pointer = c.lockedKey
		c.cardCursor = 0
	case c.pointer == "" || !slices.Contains(keys, c.pointer):
		// First tick or the pointed-at session was reaped: restart at the
		// first session's xy card.
		c.pointer = keys[0]
		c.cardCursor = 0
	default:
		// Advance within the current session's cards, else move to the next
		// session. n is resolved from the pre-advance pointer.
		n := cardsForSession(sessionByKey(snap, c.pointer))
		if c.cardCursor+1 < n {
			c.cardCursor++
		} else {
			c.pointer = pickRotated(c.pointer, keys)
			c.cardCursor = 0
		}
	}
	c.muTest.Unlock()

	c.publish(snap)
}

func (c *coordinator) publish(snap Snapshot) {
	cfg := c.loadCfg()
	lifetime := cfg.Display.FrameLifetimeSeconds
	if lifetime < 5 {
		lifetime = 5 // floor below the validated min — keeps dedupWindow positive in low-lifetime test setups
	}
	idleRestore := time.Duration(cfg.Display.IdleRestoreSeconds) * time.Second
	now := c.clk.Now()

	keys := sortedActiveKeys(snap)
	c.muTest.Lock()
	mode := c.idleStateLocked(len(keys), now, idleRestore)
	c.muTest.Unlock()

	var payload map[string]any
	switch mode {
	case idleModeActive:
		// pointer/cardCursor/locked are read without muTest: publish runs only
		// on the coordinator goroutine that also writes them; the lock exists
		// solely so tests can read this state race-free.
		payload = RenderForCoord(snap, c.pointer, c.cardCursor, c.locked, lifetime)
	case idleModeDimmed:
		payload = RenderIdleFrame(lifetime)
	case idleModeOff:
		// Countdown elapsed — let the device's lifetime expire so
		// AWTRIX scheduler returns to native apps. No publish, no
		// dedupe-state update.
		return
	}
	if payload == nil {
		return
	}

	body, mErr := json.Marshal(payload)
	if mErr != nil {
		c.logger.Error("coord payload marshal failed", "err", mErr)
		return
	}

	// Skip identical re-publishes within the dedup window. Re-POSTing
	// /api/custom restarts the firmware app's render state, which
	// resets the blinkText phase mid-cycle as a visible stutter.
	// The window must leave >= one dwell interval of margin so the next
	// tick after a skip always publishes BEFORE the device evicts the
	// app via lifetime expiry — without this, dedupe aligned with the
	// dwell-tick boundary (e.g., default lifetime=30, dwell=3) sees a
	// tick at t=27 skip, the next at t=30 publish exactly when the
	// device evicts.
	dwellSec := cfg.Display.RotationDwellSeconds
	if dwellSec <= 0 {
		dwellSec = 3
	}
	dedupWindow := time.Duration(lifetime-dwellSec-1) * time.Second
	if dedupWindow < time.Second {
		dedupWindow = time.Second
	}
	if bytes.Equal(body, c.lastPayloadBytes) && now.Sub(c.lastPublishedAt) < dedupWindow {
		return
	}

	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	err := c.publisher.CustomApp(ctx, cfg.AWTRIX.AppName, payload)
	if err != nil {
		c.logger.Warn("coord publish failed", "err", err)
		c.metrics.incPublishFail()
	} else {
		c.publishCount.Add(1)
		c.metrics.incPublishOK()
		c.lastPayloadBytes = body
		c.lastPublishedAt = now
	}
	if c.onPublishResult != nil {
		c.onPublishResult(snap, err)
	}
}
