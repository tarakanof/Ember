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
	// cmdRepublish drops the push-dedupe state and re-pushes everything at once.
	// Sent via App.RepublishAll when the device has lost (or may have lost) what
	// we pushed: a detected reboot, the boot-ping hook, or a swap to a new URL.
	cmdRepublish
)

// coordCmd is a single command sent on the buffered command channel.
type coordCmd struct {
	kind       coordCmdKind
	sessionKey string // for upsert/delete
	priorState string // for upsert: the state BEFORE this upsert (empty for new)
	newState   string // for upsert: the state AFTER this upsert
}

// coordinator owns the single goroutine that decides what AWTRIX
// payload to publish and when. Every write to the rotation (the session app,
// tiles, indicators, the display hold) passes through it. One-shot
// notifications (/v1/notify, reminders, weather and meeting popups, the
// Pomodoro phase-end alert) call the Publisher directly, and the menu's
// /v1/device proxy uses the awtrix client (device_settings.go proxyToDevice).
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

	// State owned by the goroutine. Tests read it via muTest below.
	pointer       string
	cardCursor    int
	locked        bool
	lockedKey     string
	lockEnteredAt time.Time
	// lockReleaseTimer is a wallclock-based safety net that fires a tick
	// after the attention hold (ackTimeoutDur), guaranteeing release even if
	// dwell happens to be configured larger than the hold (the tick-driven
	// check in onTick is otherwise the only release path on a sleepy
	// rotation cadence).
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

	// pomoView, when non-nil, reports the current Pomodoro render view and
	// whether a timer is active. An active Pomodoro preempts everything
	// (including attention locks): publish renders its frame into the app
	// slot and holds it. nil when the Pomodoro feature is disabled.
	pomoView func() (render.PomodoroView, bool)

	// hiddenApps, when non-nil, returns the set of tool names to omit from the
	// DEVICE display (rotation + attention lock). /state (Dashboard) is
	// unaffected — only the coordinator's render path filters.
	hiddenApps func() map[string]bool

	// indicators is what we last successfully wrote to the three corner LEDs, so
	// publish can write them on edges only (see coordinator_indicators.go).
	// Coordinator-goroutine-owned.
	indicators [3]indicatorState

	// hold is who currently owns the screen device-side. Edge-triggered: the
	// forced app switch (and, for Pomodoro, the autoTransition/blockNavigation
	// settings) are written only when this value changes, plus on a cmdRepublish
	// (a device reboot clears the pushed app behind our back). It moves only
	// once the device has accepted the edge's writes, so a lost write is
	// replayed on the next tick. Coordinator-goroutine-owned (read/written only
	// from publish/onRepublish).
	hold holdState

	// prior is the user's own autoTransition/blockNavigation, snapshotted
	// before a Pomodoro takeover and written back on release. Non-nil means a
	// takeover may be in force device-side and a restore is owed. Mirrored in
	// kv (when set) so a process that dies mid-takeover restores on its next
	// start. Coordinator-goroutine-owned once Run starts.
	prior *takeoverPrior
	// restoreBackoff counts ticks left to skip before retrying a restore that
	// was lost (see restoreBackoffTicks). Coordinator-goroutine-owned.
	restoreBackoff int
	kv             settingsKV

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

	// usage holds the latest per-tool usage snapshots; nil disables the usage
	// widget. pushedUsageApps tracks any standalone ember-usage-* apps adopted
	// from the device at startup (seeded by adoptDeviceManagedApps); they are
	// legacy leftovers from an older server and are cleared by
	// clearLegacyUsageApps on every tick. Coordinator-goroutine-owned.
	usage           *UsageStore
	pushedUsageApps map[string]pushedUsageApp

	// alarmArmed/alarmFired track the 5h limit-reset alarm per tool (key:
	// tool, value: ResetsAt epoch). In-memory by design; see checkLimitAlarms.
	// Coordinator-goroutine-owned (touched only from onTick).
	alarmArmed map[string]int64
	alarmFired map[string]int64

	// weather, when non-nil, holds the latest observation. reconcileWeatherApp
	// pushes/refreshes/clears the single "ember-weather" rotating tile from it,
	// tracked by pushedWeather (same change-and-staleness logic as usage apps).
	weather        *weatherStore
	pushedWeather  *pushedUsageApp
	pushedForecast *pushedUsageApp
	pushedAir      *pushedUsageApp

	// meetings, when non-nil, holds the upcoming occurrences. reconcileMeetingApp
	// pushes/refreshes/clears the "ember-meet" countdown tile from it, tracked by
	// pushedMeeting (same change-and-staleness logic as the weather tiles).
	meetings      *meetingsStore
	pushedMeeting *pushedUsageApp

	// adoptedApps records whether we've seeded the push trackers from the
	// device's actual app loop yet (once per process, on the first reachable
	// tick). Until then ember-managed apps left on the device by a previous run
	// are invisible to the reconcilers and never get cleared. See
	// adoptDeviceManagedApps.
	adoptedApps bool

	// lastPayloadBytes + lastPublishedAt dedupe identical re-publishes
	// within a window shorter than the AWTRIX app lifetime. Every
	// re-POST to /api/custom resets the firmware app's render state,
	// which restarts the blinkText phase mid-cycle as a visible
	// stutter; skipping no-op refreshes keeps the animation steady.
	// Only success updates these fields so failed publishes still
	// retry on the next tick.
	lastPayloadBytes []byte
	lastPublishedAt  time.Time

	// lastDropWarnNano throttles the "command dropped" warning to at most
	// ~1/min so a wedged device (onTick blocking on unreachable-device HTTP)
	// can't turn every dropped producer command into a log line. Holds the
	// wallclock UnixNano of the last emitted warning; updated with a CAS so
	// the throttle itself never blocks or allocates on the hot Send path.
	lastDropWarnNano atomic.Int64
}

// dropWarnInterval bounds how often Send logs a dropped-command warning.
const dropWarnInterval = time.Minute

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
// (atomic.Pointer[Config]) so reloadable fields (rotation_dwell_seconds) take
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
		cmds:  make(chan coordCmd, 64),
		ticks: make(chan struct{}, 1),
	}
}

// Send enqueues a command. It NEVER blocks: every caller of Send is either
// the tick ticker/timer or a producer-driven HTTP handler (handleStatus,
// handleClear, handleDeleteStatus), and the coordinator goroutine can stall
// for tens of seconds inside onTick when the device is unreachable (up to ~7
// sequential 10 s-timeout device HTTP calls). A blocking Send would wedge
// those HTTP handlers behind the coordinator, so producers time out.
//
// Stale ticks (cmdTick) drop on their 1-slot channel — the next tick picks up
// wherever the snapshot has landed. State-change commands (upsert/delete/clear)
// go to a wide 64-slot buffer; when THAT fills we drop the command rather than
// block, counting it and warning (throttled).
//
// Dropping a state-change command is an accepted tradeoff, in two parts:
//
// (a) Display state self-heals. The authoritative state lives in App.sessions
// (already updated by Upsert/Delete/Clear before Send is called), producers
// re-POST heartbeats every 10–15 s, and each dwell tick re-reads that
// snapshot: onTick's release logic (reap/drain/ack-timeout) and pointer
// advance converge the frame within ~one dwell interval, so a dropped
// delete/clear/non-attention upsert only delays the display by a tick.
//
// (b) A dropped FRESH-attention upsert loses that edge's preempt+chime
// permanently. Lock acquisition is edge-triggered and lives ONLY in onUpsert
// (attention && transition && !priorWasAttention); onTick has no acquisition
// path, and a heartbeat re-POST of a still-waiting session is
// waiting→waiting (no transition), so it cannot re-acquire. The preempt is
// gone until the session's next transition edge. Accepted because it only
// happens during a sustained >64-command burst while the coordinator is
// wedged on an unreachable device, and the session still appears in the
// normal rotation — versus back-pressuring every producer.
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
		c.metrics.incCommandDropped()
		c.warnDropThrottled(cmd)
	}
}

// warnDropThrottled logs a dropped-command warning at most once per
// dropWarnInterval. A single atomic CAS gates the log so a flood of drops
// (the exact situation that triggers this) doesn't spam the log or contend on
// a mutex on the Send hot path.
func (c *coordinator) warnDropThrottled(cmd coordCmd) {
	now := time.Now().UnixNano()
	last := c.lastDropWarnNano.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !c.lastDropWarnNano.CompareAndSwap(last, now) {
		return // another goroutine just emitted the warning
	}
	c.logger.Warn("coord: cmd channel full — dropping command (state self-heals via heartbeats/next tick)",
		"kind", cmd.kind, "session_key", cmd.sessionKey)
}

// Run is the goroutine entry point. Cancels cleanly on ctx.Done.
// The ctx is threaded into publish() so an in-flight HTTP publish
// cancels on shutdown rather than blocking on its full timeout.
// State-change commands win against ticks via channel ordering (Go's
// select is random when both are ready, so we drain cmds first
// opportunistically — preempt latency wins over rotation jitter).
func (c *coordinator) Run(ctx context.Context) {
	c.ctx = ctx
	// On shutdown, undo any active Pomodoro device takeover so a restart while
	// a timer is running doesn't leave the device with rotation + native button
	// navigation disabled.
	defer c.restorePomoTakeoverOnExit()
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
	case cmdRepublish:
		c.onRepublish()
	case cmdShutdown:
		// no-op for now
	}
}

func (c *coordinator) onUpsert(key, prior, next string) {
	attention := next == "waiting" || next == "error"
	priorWasAttention := prior == "waiting" || prior == "error"
	transition := prior != next

	freshLock := false
	c.muTest.Lock()
	switch {
	case attention && transition && !priorWasAttention && !c.keyHidden(key):
		// Fresh attention transition from a non-attention state.
		c.pointer = key
		c.cardCursor = 0
		c.locked = true
		c.lockedKey = key
		c.lockEnteredAt = c.clk.Now()
		c.armLockTimerLocked()
		freshLock = true
	case attention && transition && priorWasAttention && c.locked && c.lockedKey == key:
		// Same session shifting between waiting and error (e.g.,
		// waiting → error during an approval prompt that then failed).
		// Reset the ack timer so the new attention class gets its own
		// window — but DON'T re-target the pointer and DON'T re-chime.
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

	if freshLock && c.loadCfg().Display.AttentionChime {
		ctx := c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := c.publisher.PlayRTTTL(ctx, attentionRTTTL); err != nil {
			c.logger.Warn("attention chime failed", "err", err)
		}
	}

	if c.snapshot != nil {
		c.publish(c.filteredSnapshot())
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
		c.publish(c.filteredSnapshot())
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

	if c.snapshot != nil {
		c.publish(c.filteredSnapshot())
	}
}

// keyHidden reports whether a session key's tool segment is in the hidden set.
// Keys are "source/tool/session"; the tool is the second segment.
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

// filteredSnapshot returns the snapshot with hidden-tool sessions removed, used
// everywhere the coordinator computes the device display. nil hiddenApps or an
// empty set returns the snapshot unchanged (no copy).
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
	// Once per process, adopt the ember-managed apps already on the device so the
	// reconciles below can clear any left over from a previous run (retried on a
	// later tick if the device is unreachable now).
	if !c.adoptedApps {
		c.adoptedApps = c.adoptDeviceManagedApps()
	}
	snap := c.filteredSnapshot()
	keys := render.SortedActiveKeys(snap)

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
				if s.Key() == c.lockedKey {
					if s.State != "waiting" && s.State != "error" {
						releaseReason = "drain"
					}
					break
				}
			}
		}
		if releaseReason == "" && c.clk.Now().Sub(c.lockEnteredAt) >= c.ackTimeoutDur() {
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
		// cardCursor doubles as the idle usage-face cursor (wraps in render).
		c.cardCursor++
	case c.locked:
		// Locked: hold the target; cards never cycle during attention.
		c.pointer = c.lockedKey
		c.cardCursor = 0
	case c.pointer == "" || !slices.Contains(keys, c.pointer):
		// First tick or the pointed-at session was reaped: restart at the
		// first session's first card.
		c.pointer = keys[0]
		c.cardCursor = 0
	default:
		// Advance within the current session's cards, else move to the next
		// session. n is resolved from the pre-advance pointer. Usage views are
		// passed so card count includes any usage card that is over threshold.
		sess := render.SessionByKey(snap, c.pointer)
		n := render.CardsForSession(sess, c.usageViews(c.clk.Now(), snap)[sess.Tool])
		if c.cardCursor+1 < n {
			c.cardCursor++
		} else {
			c.pointer = render.PickRotated(c.pointer, keys)
			c.cardCursor = 0
		}
	}
	c.muTest.Unlock()

	c.publish(snap)
	c.clearLegacyUsageApps()
	c.reconcileWeatherApp(c.clk.Now())
	c.reconcileForecastApp(c.clk.Now())
	c.reconcileAirApp(c.clk.Now())
	c.reconcileMeetingApp(c.clk.Now())
	c.checkLimitAlarms(c.clk.Now(), snap)
}
