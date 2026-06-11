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

	// pomoView, when non-nil, reports the current Pomodoro render view and
	// whether a timer is active. An active Pomodoro preempts everything
	// (including attention locks): publish renders its frame into the app
	// slot and holds it. nil when the Pomodoro feature is disabled.
	pomoView func() (render.PomodoroView, bool)

	// hiddenApps, when non-nil, returns the set of tool names to omit from the
	// DEVICE display (rotation + attention lock). /state (Dashboard) is
	// unaffected — only the coordinator's render path filters.
	hiddenApps func() map[string]bool

	// pomoTakeover tracks whether the device is currently under Pomodoro
	// takeover (rotation + native button-nav disabled). Edge-triggered: the
	// ATRANS/BLOCKN settings are written only on the active↔idle transition.
	// Coordinator-goroutine-owned (read/written only from publish).
	pomoTakeover bool

	// pomoAssertedAt is when the active takeover settings/switch were last
	// pushed; publish re-asserts every pomoReassertInterval so a device reboot
	// (which clears ATRANS/BLOCKN + the forced slot) can't strand the timer in
	// the rotation. Coordinator-goroutine-owned.
	pomoAssertedAt time.Time

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
	// widget. pushedUsageApps maps each pushed usage-app name to its last-pushed
	// payload bytes + push time, so reconcileUsageApps skips unchanged re-pushes
	// (avoids device churn) BUT still refreshes before the device's lifetime
	// expires, and clears apps that drop out of the desired set. Both are
	// coordinator-goroutine-owned (touched only from onTick).
	usage           *UsageStore
	pushedUsageApps map[string]pushedUsageApp

	// weather, when non-nil, holds the latest observation. reconcileWeatherApp
	// pushes/refreshes/clears the single "ember-weather" rotating tile from it,
	// tracked by pushedWeather (same change-and-staleness logic as usage apps).
	weather        *weatherStore
	pushedWeather  *pushedUsageApp
	pushedForecast *pushedUsageApp

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
		cmds:  make(chan coordCmd, 64),
		ticks: make(chan struct{}, 1),
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
		c.cardCursor = 0
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
		// session. n is resolved from the pre-advance pointer.
		n := render.CardsForSession(render.SessionByKey(snap, c.pointer))
		if c.cardCursor+1 < n {
			c.cardCursor++
		} else {
			c.pointer = render.PickRotated(c.pointer, keys)
			c.cardCursor = 0
		}
	}
	c.muTest.Unlock()

	c.publish(snap)
	c.reconcileUsageApps(c.clk.Now(), snap)
	c.reconcileWeatherApp(c.clk.Now())
	c.reconcileForecastApp(c.clk.Now())
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

// reconcileWeatherApp pushes/refreshes the single "ember-weather" rotating tile
// when the feature is enabled, set to rotate, and has a fresh observation; it
// clears the tile otherwise. Mirrors reconcileUsageApps' change-and-staleness
// dedupe. Runs on the coordinator goroutine only.
func (c *coordinator) reconcileWeatherApp(now time.Time) {
	if c.weather == nil {
		return
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	const name = "ember-weather"
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	want := cfg.Enabled && cfg.RotateInApps && have && now.Sub(obs.FetchedAt) < weatherTileStaleTTL

	if !want {
		if c.pushedWeather != nil {
			if err := c.publisher.ClearApp(ctx, name); err != nil {
				c.logger.Warn("weather tile clear failed", "err", err)
				return
			}
			c.pushedWeather = nil
		}
		return
	}

	tempText := weatherTempText(obs.TempC, cfg.Units)
	window := forecastWindow(obs.Hourly, cfg.ForecastHours)
	var payload map[string]any
	if cfg.MoonPhase && obs.Condition == render.WeatherClear &&
		(cfg.Latitude != 0 || cfg.Longitude != 0) && isNight(cfg.Latitude, cfg.Longitude, now) {
		illum, waxing := moonIllumination(now)
		payload = render.WeatherPayloadMoon(tempText, window, render.MoonView{Illum: illum, Waxing: waxing}, usageAppLifetime)
	} else {
		payload = render.WeatherPayload(obs.Condition, tempText, window, usageAppLifetime)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if c.pushedWeather != nil && bytes.Equal(c.pushedWeather.body, body) && now.Sub(c.pushedWeather.at) < usageRefreshInterval {
		return
	}
	if err := c.publisher.CustomApp(ctx, name, payload); err != nil {
		c.logger.Warn("weather tile publish failed", "err", err)
		return
	}
	c.pushedWeather = &pushedUsageApp{body: body, at: now}
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
// on, and we have fresh hourly data; clears it otherwise. Same change-and-
// staleness dedupe as reconcileWeatherApp. Coordinator goroutine only.
func (c *coordinator) reconcileForecastApp(now time.Time) {
	if c.weather == nil {
		return
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	const name = "ember-forecast"
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	hourly := forecastWindow(obs.Hourly, cfg.ForecastHours)
	want := cfg.Enabled && cfg.ForecastTile && have && len(hourly) > 0 &&
		now.Sub(obs.FetchedAt) < weatherTileStaleTTL

	if !want {
		if c.pushedForecast != nil {
			if err := c.publisher.ClearApp(ctx, name); err != nil {
				c.logger.Warn("forecast tile clear failed", "err", err)
				return
			}
			c.pushedForecast = nil
		}
		return
	}

	payload := render.ForecastPayload(obs.Condition, weatherTempText(obs.TempC, cfg.Units), hourly, usageAppLifetime)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if c.pushedForecast != nil && bytes.Equal(c.pushedForecast.body, body) && now.Sub(c.pushedForecast.at) < usageRefreshInterval {
		return
	}
	if err := c.publisher.CustomApp(ctx, name, payload); err != nil {
		c.logger.Warn("forecast tile publish failed", "err", err)
		return
	}
	c.pushedForecast = &pushedUsageApp{body: body, at: now}
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

// usageApp pairs a device app name with the payload to push for it.
type usageApp struct {
	name    string
	payload map[string]any
}

// usageAppsToPush returns the AWTRIX apps for all visible, fresh usage windows.
// Stale (per usageStaleTTL) or hidden tools yield no apps. Percentages are
// rounded to the nearest int for the bar/threshold colour.
func usageAppsToPush(st *UsageStore, hidden map[string]bool, perModel bool, now time.Time, ttl time.Duration) []usageApp {
	var out []usageApp
	add := func(tool string, icon []string, color render.RGB) {
		if hidden[tool] || !st.Fresh(tool, now, ttl) {
			return
		}
		u, _ := st.Get(tool)
		if u.FiveHour != nil {
			out = append(out, usageApp{"ember-usage-" + tool + "-5h",
				render.UsageFiveHourPayload(icon, color, u.FiveHour.ResetLabel, pctInt(u.FiveHour.UsedPercent), usageAppLifetime)})
		}
		if u.SevenDay != nil {
			out = append(out, usageApp{"ember-usage-" + tool + "-7d",
				render.UsageWeeklyPayload(icon, color, u.SevenDay.ResetLabel, pctInt(u.SevenDay.UsedPercent), '7', 'd', usageAppLifetime)})
		}
		if perModel {
			for _, m := range []string{"opus", "sonnet"} {
				if w := u.Models[m]; w != nil {
					out = append(out, usageApp{"ember-usage-" + tool + "-" + m,
						render.UsageModelPayload(icon, color, w.ResetLabel, m, pctInt(w.UsedPercent), usageAppLifetime)})
				}
			}
		}
	}
	add("claude", render.UsageIconClaude(), render.UsageColorClaude())
	add("codex", render.UsageIconCodex(), render.UsageColorCodex())
	return out
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

// claudeFallbackApp synthesizes an ember-usage-claude-5h app from the most
// recent claude session's statusline rate data (rate_window_pct + the
// host-local rate_reset_label). Used when the authoritative endpoint usage is
// stale/absent so the 5h frame still shows while a Claude session is live.
// snap is the already-tool-filtered snapshot, so a hidden claude tool yields no
// session here and thus no fallback. ok=false when no usable session exists.
func claudeFallbackApp(snap Snapshot) (usageApp, bool) {
	var best *render.Session
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		if s.Tool != "claude" || s.RateWindowPct == nil || s.RateResetLabel == "" {
			continue
		}
		if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
			best = s
		}
	}
	if best == nil {
		return usageApp{}, false
	}
	payload := render.UsageFiveHourPayload(render.UsageIconClaude(), render.UsageColorClaude(),
		best.RateResetLabel, *best.RateWindowPct, usageAppLifetime)
	return usageApp{"ember-usage-claude-5h", payload}, true
}

// reconcileUsageApps pushes the desired set of usage apps to the device and
// clears any previously-pushed app that is no longer desired. Unchanged apps
// are skipped (payload-bytes diff) so the device isn't re-POSTed every tick.
// No-op when the widget is disabled or no store is wired. Runs on the
// coordinator goroutine only.
func (c *coordinator) reconcileUsageApps(now time.Time, snap Snapshot) {
	if c.usage == nil {
		return
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := c.loadCfg()
	var desired []usageApp
	if cfg.usageWidgetEnabled() {
		var hidden map[string]bool
		if c.hiddenApps != nil {
			hidden = c.hiddenApps()
		}
		desired = usageAppsToPush(c.usage, hidden, cfg.usagePerModelEnabled(), now, usageStaleTTL)
		// Claude 5h fallback: when the endpoint path produced no claude-5h app
		// (usage stale/absent), synthesise it from the live session's statusline
		// data so the 5h frame doesn't blank while Claude is active.
		hasClaude5h := false
		for _, a := range desired {
			if a.name == "ember-usage-claude-5h" {
				hasClaude5h = true
				break
			}
		}
		if !hasClaude5h {
			if app, ok := claudeFallbackApp(snap); ok {
				desired = append(desired, app)
			}
		}
	}
	seen := make(map[string]bool, len(desired))
	for _, app := range desired {
		seen[app.name] = true
		body, err := json.Marshal(app.payload)
		if err != nil {
			continue
		}
		// Skip the re-push only when the payload is unchanged AND it was pushed
		// recently enough that the device hasn't evicted it (lifetime). Without
		// the time bound, a usage value that stops changing would expire on the
		// device and never be refreshed.
		if prev, ok := c.pushedUsageApps[app.name]; ok && bytes.Equal(prev.body, body) && now.Sub(prev.at) < usageRefreshInterval {
			continue
		}
		if err := c.publisher.CustomApp(ctx, app.name, app.payload); err != nil {
			c.logger.Warn("usage app publish failed", "name", app.name, "err", err)
			continue
		}
		if c.pushedUsageApps == nil {
			c.pushedUsageApps = map[string]pushedUsageApp{}
		}
		c.pushedUsageApps[app.name] = pushedUsageApp{body: body, at: now}
	}
	for name := range c.pushedUsageApps {
		if seen[name] {
			continue
		}
		if err := c.publisher.ClearApp(ctx, name); err != nil {
			c.logger.Warn("usage app clear failed", "name", name, "err", err)
			continue
		}
		delete(c.pushedUsageApps, name)
	}
}

// pomoReassertInterval bounds how often an ACTIVE takeover re-pushes its
// device settings + app switch. The takeover is otherwise edge-triggered, but
// a device reboot silently clears ATRANS/BLOCKN and the forced app slot while
// the coordinator's flag stays set — so the timer would rotate as a normal app
// instead of holding the screen. Re-asserting on this cadence recovers the
// takeover within one interval of a reboot without churning the device on
// every publish.
const pomoReassertInterval = 30 * time.Second

// applyPomoTakeover writes the device rotation/native-nav settings on the
// active↔idle Pomodoro edge. While active: disable app rotation (ATRANS) and
// native button navigation (BLOCKN) and force the device to the app slot so
// the timer holds the screen and the buttons drive the timer instead of
// switching apps. On return to idle: restore both. While the takeover stays
// active it is also re-asserted every pomoReassertInterval (reboot recovery,
// see the const). Runs on the coordinator goroutine only.
func (c *coordinator) applyPomoTakeover(active bool, appName string, now time.Time) {
	if active == c.pomoTakeover {
		// Steady state: re-assert periodically so a device reboot can't strand
		// the timer in the rotation. Idle steady state needs nothing.
		if active && now.Sub(c.pomoAssertedAt) >= pomoReassertInterval {
			c.assertPomoTakeover(appName, now)
		}
		return
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if active {
		c.assertPomoTakeover(appName, now)
	} else {
		if err := c.publisher.Settings(ctx, map[string]any{"ATRANS": true, "BLOCKN": false}); err != nil {
			c.logger.Warn("pomo restore settings failed", "err", err)
		}
	}
	c.pomoTakeover = active
}

// assertPomoTakeover (re-)issues the takeover settings + forced app switch and
// records the time. Caller ensures a timer is active.
func (c *coordinator) assertPomoTakeover(appName string, now time.Time) {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.publisher.Settings(ctx, map[string]any{"ATRANS": false, "BLOCKN": true}); err != nil {
		c.logger.Warn("pomo takeover settings failed", "err", err)
	}
	if err := c.publisher.Switch(ctx, appName); err != nil {
		c.logger.Warn("pomo switch failed", "err", err)
	}
	c.pomoAssertedAt = now
}

// restorePomoTakeoverOnExit re-enables app rotation + native button navigation
// if a Pomodoro takeover was active when the coordinator stops. Uses a fresh
// context because the Run context is already cancelled on exit.
func (c *coordinator) restorePomoTakeoverOnExit() {
	if !c.pomoTakeover {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.publisher.Settings(ctx, map[string]any{"ATRANS": true, "BLOCKN": false}); err != nil {
		c.logger.Warn("pomo restore on shutdown failed", "err", err)
	}
	c.pomoTakeover = false
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
	// render its frame and apply device takeover (edge-triggered). When it
	// goes idle, restore rotation/native-nav and fall through to the normal
	// session rendering below.
	var pomoActive bool
	var payload map[string]any
	if c.pomoView != nil {
		if view, on := c.pomoView(); on {
			pomoActive = true
			payload = render.PomodoroPayload(view, lifetime)
		}
	}
	c.applyPomoTakeover(pomoActive, cfg.AWTRIX.AppName, now)

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
			payload = render.RenderForCoord(snap, c.pointer, c.cardCursor, c.locked, lifetime)
		case idleModeDimmed:
			payload = render.RenderIdleFrame(lifetime)
		case idleModeOff:
			// Countdown elapsed — let the device's lifetime expire so
			// AWTRIX scheduler returns to native apps. No publish, no
			// dedupe-state update.
			return
		}
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
