package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	// Sent when the device is known to have lost what we pushed — today only a
	// detected reboot (App.RepublishAll).
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

	// hold is who currently owns the screen device-side. Edge-triggered: the
	// forced app switch (and, for Pomodoro, the autoTransition/blockNavigation
	// settings) are written only when this value changes, plus on a cmdRepublish
	// (a device reboot clears both behind our back). Coordinator-goroutine-owned
	// (read/written only from publish/onRepublish).
	hold holdState

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

// attentionRTTTL is the optional lock-acquisition chime (display.attention_chime).
const attentionRTTTL = "attn:d=16,o=6,b=200:c,e,g"

// ackTimeoutDur reads the attention-hold duration live so a /v1/display/config
// PUT applies to the CURRENT lock without restart.
func (c *coordinator) ackTimeoutDur() time.Duration {
	sec := c.loadCfg().Display.AckTimeoutSeconds
	if sec <= 0 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

// armLockTimerLocked installs (or replaces) the wallclock safety-net
// timer that fires a cmdTick after the current ackTimeoutDur. Caller must hold muTest.
// The timer is armed with the value at arm time; if the hold is shortened
// mid-lock via a config PUT, the tick-driven release check in onTick still
// releases promptly (it re-reads live on every tick), but this safety-net
// timer may fire later than the new shorter value.
func (c *coordinator) armLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
	}
	c.lockReleaseTimer = time.AfterFunc(c.ackTimeoutDur(), func() {
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

// adoptDeviceManagedApps seeds the in-memory push trackers from the apps
// actually present on the device, so ember-managed custom apps (weather,
// forecast, usage) left over from a previous process can be reconciled — and
// cleared when no longer wanted — even though the trackers start empty after a
// restart. Each adopted entry is seeded as stale (zero payload + time) so the
// normal reconcile re-pushes it if still desired, or clears it if not. The base
// rotating app and native apps (Time, etc.) are deliberately left untouched.
// Returns false if the device loop can't be read, so the caller retries on a
// later tick once the device is reachable. Runs on the coordinator goroutine.
func (c *coordinator) adoptDeviceManagedApps() bool {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	names, err := c.publisher.ListApps(ctx)
	if err != nil {
		c.logger.Warn("device app loop read failed; deferring adopt", "err", err)
		return false
	}
	baseApp := c.loadCfg().AWTRIX.AppName
	for _, name := range names {
		switch {
		case name == baseApp:
			// The main rotating app is owned by publish(), not the reconcilers.
		case name == "ember-weather":
			if c.pushedWeather == nil {
				c.pushedWeather = &pushedUsageApp{}
			}
		case name == "ember-forecast":
			if c.pushedForecast == nil {
				c.pushedForecast = &pushedUsageApp{}
			}
		case name == "ember-air":
			if c.pushedAir == nil {
				c.pushedAir = &pushedUsageApp{}
			}
		case name == "ember-meet":
			if c.pushedMeeting == nil {
				c.pushedMeeting = &pushedUsageApp{}
			}
		case strings.HasPrefix(name, "ember-usage-"):
			if c.pushedUsageApps == nil {
				c.pushedUsageApps = map[string]pushedUsageApp{}
			}
			if _, ok := c.pushedUsageApps[name]; !ok {
				c.pushedUsageApps[name] = pushedUsageApp{}
			}
		}
	}
	return true
}

// weatherTileStaleTTL clears the weather tile if no fresh observation arrived
// within this window (≈3× the default 10-min poll), so a wedged poller doesn't
// leave a stale temperature on the device indefinitely.
const weatherTileStaleTTL = 30 * time.Minute

// reconcileTile owns the shared clear/dedupe/push state machine for the
// standalone rotating tiles (weather/forecast/air/meeting). The per-tile
// decisions — whether the tile should be on the device and what it shows —
// stay at the call sites: want gates, buildPayload runs only when want is
// true. tracker is the per-tile *pushedUsageApp pointer-to-pointer so this
// helper can nil it on clear. Coordinator goroutine only.
func (c *coordinator) reconcileTile(now time.Time, name string, tracker **pushedUsageApp, want bool, buildPayload func() map[string]any) {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !want {
		if *tracker != nil {
			if err := c.publisher.ClearApp(ctx, name); err != nil {
				c.logger.Warn("tile clear failed", "app", name, "err", err)
				return
			}
			*tracker = nil
		}
		return
	}
	payload := buildPayload()
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Warn("tile payload marshal failed", "app", name, "err", err)
		return
	}
	if *tracker != nil && bytes.Equal((*tracker).body, body) && now.Sub((*tracker).at) < usageRefreshInterval {
		return
	}
	if err := c.publisher.CustomApp(ctx, name, payload); err != nil {
		c.logger.Warn("tile publish failed", "app", name, "err", err)
		return
	}
	*tracker = &pushedUsageApp{body: body, at: now}
}

// reconcileWeatherApp pushes/refreshes the single "ember-weather" rotating tile
// when the feature is enabled, set to rotate, and has a fresh observation; it
// clears the tile otherwise. Coordinator goroutine only.
func (c *coordinator) reconcileWeatherApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	want := cfg.Enabled && cfg.RotateInAppsEnabled() && have && now.Sub(obs.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-weather", &c.pushedWeather, want, func() map[string]any {
		tempText := weatherTempText(obs.TempC, cfg.Units)
		window := forecastWindow(obs.Hourly, cfg.ForecastHours)
		switch {
		case cfg.MoonPhaseEnabled() && obs.Condition == render.WeatherClear &&
			(cfg.Latitude != 0 || cfg.Longitude != 0) && isNight(cfg.Latitude, cfg.Longitude, now):
			// Moon wins over native icons — there is no per-phase gallery set.
			illum, waxing := moonIllumination(now)
			return render.WeatherPayloadMoon(tempText, window, render.MoonView{Illum: illum, Waxing: waxing}, usageAppLifetime)
		case cfg.TileNativeIcons:
			return render.WeatherPayloadNative(cfg.weatherIconID(obs.Condition), tempText, window, usageAppLifetime)
		default:
			return render.WeatherPayload(obs.Condition, tempText, window, usageAppLifetime)
		}
	})
}

// forecastWindow returns the first `hours` hourly temps (hours clamped to a sane
// 1..24), or the whole slice when shorter. nil/empty in → nil out.
func forecastWindow(hourly []float64, hours int) []float64 {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24 {
		hours = 24
	}
	if len(hourly) > hours {
		return hourly[:hours]
	}
	return hourly
}

// reconcileForecastApp pushes/refreshes the standalone "ember-forecast" tile
// (hourly temperature bars) when weather is enabled, the forecast tile is turned
// on, and we have fresh hourly data; clears it otherwise. Coordinator goroutine only.
func (c *coordinator) reconcileForecastApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	hourly := forecastWindow(obs.Hourly, cfg.ForecastHours)
	want := cfg.Enabled && cfg.ForecastTileEnabled() && have && len(hourly) > 0 &&
		now.Sub(obs.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-forecast", &c.pushedForecast, want, func() map[string]any {
		return render.ForecastPayload(hourly, usageAppLifetime)
	})
}

// reconcileAirApp pushes/refreshes the standalone "ember-air" tile (current
// European AQI + hourly trend strip) when weather is enabled, the air tile is
// turned on, and the air observation is fresh; clears it otherwise. Coordinator
// goroutine only.
func (c *coordinator) reconcileAirApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	air, have := c.weather.currentAir()
	want := cfg.Enabled && cfg.AirTileEnabled() && have && now.Sub(air.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-air", &c.pushedAir, want, func() map[string]any {
		return render.AirPayload(air.AQI, air.HourlyAQI, usageAppLifetime)
	})
}

// meetingMinutes is the displayed whole-minute countdown: ceil(remaining), min 1.
// The tile never shows "0m" — it leaves the rotation at start.
func meetingMinutes(now, start time.Time) int {
	m := int((start.Sub(now) + time.Minute - 1) / time.Minute)
	if m < 1 {
		m = 1
	}
	return m
}

// reconcileMeetingApp pushes/refreshes the standalone "ember-meet" countdown
// tile when meetings are enabled, the next meeting is inside the lead window,
// and the feed data is fresh; clears it otherwise (including at meeting start —
// the countdown never shows 0m). The minute-by-minute countdown needs no timer:
// the payload text changes each minute, so the bytes-diff naturally re-pushes.
// Coordinator goroutine only.
func (c *coordinator) reconcileMeetingApp(now time.Time) {
	if c.meetings == nil {
		return
	}
	cfg := c.loadCfg().Meetings
	occ, ok := c.meetings.next(now)
	want := cfg.IsEnabled() && ok && c.meetings.fresh(now) &&
		occ.Start.Sub(now) <= time.Duration(cfg.TileLeadMinutes)*time.Minute
	c.reconcileTile(now, "ember-meet", &c.pushedMeeting, want, func() map[string]any {
		return render.MeetingPayload(sanitizeMeetingTitle(occ.Title), meetingMinutes(now, occ.Start), usageAppLifetime)
	})
}

const (
	// usageAppLifetime keeps a pushed usage app alive on the device well above
	// the ~5-min producer refresh, so a brief reconcile gap never blanks it.
	usageAppLifetime = 600 // seconds
	// usageStaleTTL is ~2x the 5-min poll interval: past this with no fresh
	// post, a tool's apps are cleared from the device.
	usageStaleTTL = 10 * time.Minute
	// usageRefreshInterval forces a re-push of an unchanged usage app well
	// before its on-device lifetime (usageAppLifetime) expires — otherwise a
	// usage value that stops changing would let the device evict the app and
	// never get refreshed. Must be < usageAppLifetime.
	usageRefreshInterval = 4 * time.Minute
)

// pushedUsageApp records the payload bytes + push time of a usage app last sent
// to the device, for change-and-staleness-aware re-push.
type pushedUsageApp struct {
	body []byte
	at   time.Time
}

// pctInt rounds a float utilization to the nearest int, clamped to 0..100.
func pctInt(f float64) int {
	n := int(f + 0.5)
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// usageViews builds the per-tool usage views the render layer consumes:
// endpoint usage preferred, statusline fallback (same precedence as the
// limit alarm via effectiveFiveHour), gated at usage_threshold_pct. Hidden
// tools and below-threshold tools are absent. Returns nil when the widget
// is off or no store is wired.
func (c *coordinator) usageViews(now time.Time, snap Snapshot) map[string]*render.UsageView {
	cfg := c.loadCfg()
	if c.usage == nil || !cfg.usageWidgetEnabled() {
		return nil
	}
	thr := cfg.usageThresholdPct()
	var hidden map[string]bool
	if c.hiddenApps != nil {
		hidden = c.hiddenApps()
	}
	views := map[string]*render.UsageView{}
	for _, tool := range []string{"claude", "codex"} {
		if hidden[tool] {
			continue
		}
		pct, resetAt, ok := effectiveFiveHour(c.usage, snap, tool, now)
		if !ok || pctInt(pct) < thr {
			continue
		}
		v := &render.UsageView{FiveHourPct: pctInt(pct), ResetAt: resetAt}
		if c.usage.Fresh(tool, now, usageStaleTTL) {
			u, _ := c.usage.Get(tool)
			if u.FiveHour != nil {
				v.ResetLabel = u.FiveHour.ResetLabel
			}
			if u.SevenDay != nil {
				p := pctInt(u.SevenDay.UsedPercent)
				v.SevenDayPct = &p
			}
			if cfg.usagePerModelEnabled() {
				for _, m := range []string{"opus", "sonnet"} {
					if w := u.Models[m]; w != nil {
						marker := "OP"
						if m == "sonnet" {
							marker = "SO"
						}
						v.Models = append(v.Models, render.ModelUsage{Marker: marker, Pct: pctInt(w.UsedPercent)})
					}
				}
			}
		} else {
			// Statusline fallback: the newest live session's host-local label.
			// effectiveFiveHour already accepted a session, but didn't give us
			// the label — find the same best session to populate ResetLabel.
			var best *render.Session
			for i := range snap.Sessions {
				s := &snap.Sessions[i]
				if s.Tool != tool || s.RateResetLabel == "" {
					continue
				}
				if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
					best = s
				}
			}
			if best != nil {
				v.ResetLabel = best.RateResetLabel
			}
		}
		views[tool] = v
	}
	return views
}

// clearLegacyUsageApps removes any standalone ember-usage-* apps from the
// device. Usage now renders inside the main app (usage card / idle usage
// frame); the only standalone apps left to handle are leftovers from an
// older server version, seeded into pushedUsageApps by
// adoptDeviceManagedApps. Failed clears stay tracked and retry next tick.
func (c *coordinator) clearLegacyUsageApps() {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for name := range c.pushedUsageApps {
		if err := c.publisher.ClearApp(ctx, name); err != nil {
			c.logger.Warn("legacy usage app clear failed", "name", name, "err", err)
			continue
		}
		delete(c.pushedUsageApps, name)
	}
}

// holdState is who owns the screen device-side, in precedence order.
type holdState int

const (
	// holdNone: the ember app takes its turn in the device's app loop like any
	// other tile, and no device setting is overridden. This covers the idle
	// frames too: their payload asks for a long dwell so they linger once the
	// rotation reaches them, but nothing about them is urgent enough to justify
	// pushing the clock's own apps off the screen.
	holdNone holdState = iota
	// holdAttention: a locked waiting/error frame needs the screen now, so the
	// app is force-switched to and its own long durationMs keeps it there for
	// the attention window.
	holdAttention
	// holdPomodoro: a running timer owns the screen for its whole phase, which
	// outlasts any dwell, so the device's rotation and native button navigation
	// are disabled for the duration.
	holdPomodoro
)

// takeoverSettings is the pair of device settings a Pomodoro takeover flips.
func takeoverSettings(on bool) map[string]any {
	return map[string]any{"autoTransition": !on, "blockNavigation": on}
}

// applyDisplayHold moves the device to the requested screen owner, writing only
// on the edge — a per-tick re-assert would spam apps/active and re-trigger the
// transition animation every dwell.
//
// awtrix-ng has no per-payload priority (AWTRIX3's prio+force 422 on NG), and a
// pushed app's own durationMs only takes effect once the rotation reaches its
// slot. So "this frame must own the screen NOW" is a forced PUT
// /api/v1/apps/active, which the app's long durationMs then sustains. Measured
// on firmware 1.0.13 against a live 7-app rotation: switch + durationMs=30000
// held 31 s; switch + durationMs=6000 held 6.7 s; so the switch supplies the
// jump and durationMs supplies the length.
//
// That is enough for a 30 s attention window but not for a 25-minute focus
// block, which is why holdPomodoro additionally sets autoTransition:false
// (verified to outrank the per-app dwell entirely) plus blockNavigation:true so
// the buttons drive the timer instead of the app loop. holdAttention
// deliberately leaves both settings alone: it stays crash-safe, since a server
// that dies mid-hold leaves the clock to expire the dwell and resume rotation
// on its own.
//
// Recovery from a device reboot (which drops the pushed apps and both settings)
// arrives as a cmdRepublish, which fakes the edge by resetting c.hold. Runs on
// the coordinator goroutine only.
func (c *coordinator) applyDisplayHold(want holdState, appName string) {
	if want == c.hold {
		return
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	prev := c.hold
	c.hold = want
	if prev == holdPomodoro {
		if err := c.publisher.Settings(ctx, takeoverSettings(false)); err != nil {
			c.logger.Warn("display hold restore settings failed", "err", err)
		}
	}
	if want == holdPomodoro {
		if err := c.publisher.Settings(ctx, takeoverSettings(true)); err != nil {
			c.logger.Warn("display hold takeover settings failed", "err", err)
		}
	}
	if want != holdNone {
		if err := c.publisher.Switch(ctx, appName); err != nil {
			c.logger.Warn("display hold switch failed", "err", err, "hold", want)
		}
	}
}

// restorePomoTakeoverOnExit re-enables app rotation + native button navigation
// if a Pomodoro takeover was in force when the coordinator stops. Uses a fresh
// context because the Run context is already cancelled on exit. A holdAttention
// needs no undo — nothing sticky was written for it.
func (c *coordinator) restorePomoTakeoverOnExit() {
	if c.hold != holdPomodoro {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.publisher.Settings(ctx, takeoverSettings(false)); err != nil {
		c.logger.Warn("pomo restore on shutdown failed", "err", err)
	}
	c.hold = holdNone
}

// onRepublish forgets everything we believe the device is currently showing and
// runs a full cycle on the spot. Pushed apps are RAM-only on awtrix-ng: after a
// reboot the device holds none of them, while the dedupe caches below would
// happily suppress a re-push for a whole frame lifetime (and a tile whose
// content never changes would never come back at all). Dropping c.hold turns the
// next applyDisplayHold into a fresh edge, so an in-flight focus block or
// attention hold re-asserts its forced app switch (and, for Pomodoro, its
// autoTransition/blockNavigation) too. Coordinator goroutine only.
func (c *coordinator) onRepublish() {
	c.lastPayloadBytes = nil
	c.lastPublishedAt = time.Time{}
	c.pushedWeather, c.pushedForecast, c.pushedAir, c.pushedMeeting = nil, nil, nil, nil
	// Legacy standalone usage apps died with the reboot; nothing left to clear.
	c.pushedUsageApps = nil
	c.hold = holdNone
	c.onTick()
}

func (c *coordinator) publish(snap Snapshot) {
	cfg := c.loadCfg()
	lifetime := cfg.Display.FrameLifetimeSeconds
	if lifetime < 5 {
		lifetime = 5 // floor below the validated min — keeps dedupWindow positive in low-lifetime test setups
	}
	idleRestore := time.Duration(cfg.Display.IdleRestoreSeconds) * time.Second
	now := c.clk.Now()

	// Pomodoro preempt (highest priority). An active timer owns the display:
	// render its frame and take the device over for the whole phase. When it
	// goes idle, fall through to the normal session rendering below, which may
	// itself want the (weaker) frame hold.
	var pomoActive bool
	var payload map[string]any
	// want is the device-level screen owner this frame asks for; it is applied
	// only once the payload is known to be on the device (see below).
	want := holdNone
	if c.pomoView != nil {
		if view, on := c.pomoView(); on {
			pomoActive = true
			payload = render.PomodoroPayload(view, lifetime)
			want = holdPomodoro
		}
	}

	if !pomoActive {
		keys := render.SortedActiveKeys(snap)
		c.muTest.Lock()
		mode := c.idleStateLocked(len(keys), now, idleRestore)
		c.muTest.Unlock()

		switch mode {
		case idleModeActive:
			// pointer/cardCursor/locked are read without muTest: publish runs only
			// on the coordinator goroutine that also writes them; the lock exists
			// solely so tests can read this state race-free.
			payload = render.RenderForCoord(snap, c.pointer, c.cardCursor, c.locked, lifetime, c.usageViews(now, snap))
			if render.AttentionHeld(snap, c.pointer, c.locked) {
				want = holdAttention
			}
		case idleModeDimmed:
			payload = render.RenderIdleFrame(lifetime)
		case idleModeOff:
			// Countdown elapsed. If a tool's 5h window is over the usage
			// threshold, keep the slot alive with the dimmed usage frame so a
			// hot window stays visible while the user is away. Otherwise let
			// the device's lifetime expire (AWTRIX returns to native apps).
			payload = render.RenderIdleUsagePayload(c.usageViews(now, snap), c.cardCursor, now, lifetime)
		}
	}
	if payload == nil {
		// Nothing to show — release the hold so the rotation (and, after a
		// Pomodoro takeover, the device's own settings) come back.
		c.applyDisplayHold(holdNone, cfg.AWTRIX.AppName)
		return
	}

	body, mErr := json.Marshal(payload)
	if mErr != nil {
		c.logger.Error("coord payload marshal failed", "err", mErr)
		return
	}

	// Skip identical re-publishes within the dedup window.
	//
	// The original reason — AWTRIX3 reset a re-POSTed app's render state, so an
	// unchanged re-push restarted the blinking label mid-cycle as a visible
	// stutter — does NOT apply to awtrix-ng. Measured on firmware 1.0.13: 20
	// re-pushes of a byte-identical textBlinkMs:1000 payload over 6 s (i.e. more
	// often than the 500 ms half-period) left the blink alternating on its
	// original phase, and an app re-pushed every 2 s yielded its slot at the same
	// ~8.7 s dwell as one pushed once. A re-push is idempotent for both animation
	// phase and dwell timing.
	//
	// It stays because the work it avoids is real: an unchanged frame otherwise
	// costs a JSON push (up to ~2.4 KB of bitmap) to the ESP32 on every rotation
	// tick, parsed on the same task that drives the panel.
	//
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
		// Same frame, already on the device: the hold edge may still be new
		// (e.g. a Pomodoro pause that leaves the payload byte-identical).
		c.applyDisplayHold(want, cfg.AWTRIX.AppName)
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
		// Only now is the app known to be in the device's loop — apps/active
		// 404s on an app the device does not have.
		c.applyDisplayHold(want, cfg.AWTRIX.AppName)
	}
	if c.onPublishResult != nil {
		c.onPublishResult(snap, err)
	}
}
