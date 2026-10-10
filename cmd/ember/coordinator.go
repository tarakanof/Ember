package main

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type coordCmdKind int

const (
	cmdTick coordCmdKind = iota
	cmdUpsert
	cmdDelete
	cmdClear
	cmdShutdown
	cmdRepublish
)

type coordCmd struct {
	kind       coordCmdKind
	sessionKey string
	priorState string
	newState   string
}

type coordinator struct {
	loadCfg   func() *Config
	publisher Publisher
	clk       clock
	logger    *slog.Logger
	metrics   *metrics

	cmds  chan coordCmd
	ticks chan struct{}

	pointer    string
	cardCursor int
	attentionState
	lockReleaseTimer *time.Timer

	idleSince time.Time

	snapshot func() Snapshot

	pomoView func() (render.PomodoroView, bool)

	hiddenApps func() map[string]bool

	indicators [3]indicatorState

	hold holdState

	prior          *takeoverPrior
	priorMu        ctxLock
	priorGen       uint64
	editSeq        uint64
	keySeq         map[string]uint64
	keyVal         map[string]any
	restoreBackoff int
	kv             settingsKV

	onPublishResult func(snap Snapshot, err error)

	ctx context.Context

	stateMu sync.RWMutex

	publishCount atomic.Int64

	usage *UsageStore

	limitAlarmState

	weather  *weatherStore
	meetings *meetingsStore

	tiles tileSet

	adoptedApps bool

	mainPushed pushedApp

	lastDropWarnNano atomic.Int64
}

const dropWarnInterval = time.Minute

type coordIdleMode int

const (
	idleModeActive coordIdleMode = iota
	idleModeDimmed
	idleModeOff
)

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
		priorMu:   newCtxLock(),
		publisher: publisher,
		clk:       clk,
		logger:    logger,
		metrics:   m,
		cmds:      make(chan coordCmd, 64),
		ticks:     make(chan struct{}, 1),
		tiles:     tileSet{logger: logger},
	}
}

func (c *coordinator) Send(cmd coordCmd) {
	if cmd.kind == cmdTick {
		select {
		case c.ticks <- struct{}{}:
		default:
		}
		return
	}
	select {
	case c.cmds <- cmd:
	default:
		c.metrics.incCommandDropped()
		c.warnDropThrottled(cmd)
	}
}

func (c *coordinator) warnDropThrottled(cmd coordCmd) {
	now := time.Now().UnixNano()
	last := c.lastDropWarnNano.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !c.lastDropWarnNano.CompareAndSwap(last, now) {
		return
	}
	c.logger.Warn("coord: cmd channel full — dropping command (state self-heals via heartbeats/next tick)",
		"kind", cmd.kind, "session_key", cmd.sessionKey)
}

func (c *coordinator) Run(ctx context.Context) {
	c.ctx = ctx
	defer c.restorePomoTakeoverOnExit()
	for {
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
	case cmdRepublish:
		c.onRepublish()
	case cmdShutdown:
	}
}

func (c *coordinator) onUpsert(key, prior, next string) {
	step := c.attentionState.onTransition(key, prior, next)
	if step == attentionAcquire && c.keyHidden(key) {
		step = attentionKeep
	}
	c.stateMu.Lock()
	c.applyAttentionLocked(step, key)
	c.stateMu.Unlock()

	if step == attentionAcquire && c.loadCfg().Display.AttentionChime {
		ctx := c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := c.playChime(ctx, attentionRTTTL); err != nil {
			c.logger.Warn("attention chime failed", "err", err)
		}
	}

	if c.snapshot != nil {
		c.publish(c.filteredSnapshot())
	}
}

func (c *coordinator) onDelete(key string) {
	c.stateMu.Lock()
	if c.attentionState.holds(key) {
		c.attentionState.release()
		c.disarmLockTimerLocked()
	}
	if c.pointer == key {
		c.pointer = ""
		c.cardCursor = 0
	}
	c.stateMu.Unlock()
	if c.snapshot != nil {
		c.publish(c.filteredSnapshot())
	}
}

func (c *coordinator) onClear() {
	c.stateMu.Lock()
	c.pointer = ""
	c.cardCursor = 0
	c.attentionState.release()
	c.disarmLockTimerLocked()
	c.stateMu.Unlock()

	if c.snapshot != nil {
		c.publish(c.filteredSnapshot())
	}
}

func (c *coordinator) keyHidden(key string) bool {
	if c.hiddenApps == nil {
		return false
	}
	hidden := c.hiddenApps()
	if len(hidden) == 0 {
		return false
	}
	parts := strings.SplitN(key, "/", 3)
	return len(parts) >= 2 && hidden[parts[1]]
}

func (c *coordinator) filteredSnapshot() Snapshot {
	snap := c.snapshot()
	if c.hiddenApps == nil {
		return snap
	}
	hidden := c.hiddenApps()
	if len(hidden) == 0 {
		return snap
	}
	kept := make([]render.Session, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		if !hidden[s.Tool] {
			kept = append(kept, s)
		}
	}
	snap.Sessions = kept
	return snap
}

func (c *coordinator) onTick() {
	if c.snapshot == nil {
		return
	}
	if !c.adoptedApps {
		c.adoptedApps = c.adoptDeviceManagedApps()
	}
	snap := c.filteredSnapshot()
	keys := render.SortedActiveKeys(snap)

	if c.locked {
		if end := c.attentionState.ended(keys, snap.Sessions, c.clk.Now(), c.ackTimeoutDur()); end != attentionHeld {
			c.stateMu.Lock()
			c.releaseLockLocked(end)
			c.stateMu.Unlock()
		}
	}

	c.stateMu.Lock()
	switch {
	case len(keys) == 0:
		c.pointer = ""
		c.cardCursor++
	case c.locked:
		c.pointer = c.lockedKey
		c.cardCursor = 0
	case c.pointer == "" || !slices.Contains(keys, c.pointer):
		c.pointer = keys[0]
		c.cardCursor = 0
	default:
		sess := render.SessionByKey(snap, c.pointer)
		n := render.CardsForSession(sess, c.usageViews(c.clk.Now(), snap)[sess.Tool])
		if c.cardCursor+1 < n {
			c.cardCursor++
		} else {
			c.pointer = render.PickRotated(c.pointer, keys)
			c.cardCursor = 0
		}
	}
	c.stateMu.Unlock()

	c.publish(snap)
	c.reconcileTiles(c.clk.Now())
	c.checkLimitAlarms(c.clk.Now(), snap)
}
