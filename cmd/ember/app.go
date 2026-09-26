package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/sessions"
)

type App struct {
	cfg          atomic.Pointer[Config] // hot-swappable; read with cfg.Load() per request
	cfgMu        sync.Mutex             // serializes cfg's read-copy-write; see updateConfig
	configPath   string                 // resolved at startup; "" when running on defaults
	configSource string                 // "flag" | "env" | "cwd" | "defaults"
	publisher    Publisher              // server-initiated writes, quiet-gated; the coordinator holds the same one
	clock        *clockAccess           // every other clock call (menu proxy, probes, doctor); see clock_access.go
	logger       *slog.Logger
	listener     net.Listener // bound HTTP listener; captured at startup for doctor introspection
	versionInfo  versionInfo  // computed once at startup; served by /version
	startedAt    time.Time    // set in NewApp; used by doctor uptime check
	limiter      *IPLimiter   // populated in NewApp; sweeper started by main()

	sessions *sessions.Registry // live producer sessions; reaps on every access

	mu            sync.Mutex // protects lastPublished, lastPublish*
	lastPublished Render

	// Last-publish telemetry, all guarded by App.mu.
	lastPublishAt  time.Time
	lastPublishOK  bool
	lastPublishErr string

	metrics *metrics // populated by NewApp; never nil at runtime
	coord   *coordinator

	// engine + store are non-nil only when the Pomodoro feature is enabled
	// (wired via EnablePomodoro). The engine is safe for concurrent use; the
	// store is single-writer (driven from the coordinator/HTTP path).
	engine *pomodoro.Engine
	store  *pomodoro.Store

	// reminderHeldUntil is the unix-nano deadline during which a hold:true reminder
	// alarm is assumed to be on the clock. While armed, a device button press is
	// treated as acknowledging the alarm (the firmware dismisses on the middle
	// button) rather than a Pomodoro action — the middle press disarms it. 0 = none.
	reminderHeldUntil atomic.Int64
	// reminderLoop is the held alarm whose chime is looping, if any; see
	// checkReminderLoop.
	reminderLoop reminderLoop

	// reminderKeys dedupes POST /v1/reminders/fire retries by Idempotency-Key.
	reminderKeys reminderDedupe

	// activityLast throttles activity-heartbeat persistence to at most one row
	// per session per activityThrottle window (producers post every 2-10s, far
	// finer than the work-hours sessionization needs); a transition into
	// waiting bypasses it (see recordActivityHeartbeat). activitySweptAt is the
	// last time expired entries were dropped. Both guarded by activityMu.
	activityMu      sync.Mutex
	activityLast    map[string]activityMark
	activitySweptAt time.Time

	statsCache statsCache // last GET /v1/pomodoro/stats payload

	// settings is the runtime-settings overlay: every menu-editable config
	// slice, merged over the config.json baseline and persisted to store.
	settings appSettings

	appsMu     sync.Mutex      // guards hiddenApps
	hiddenApps map[string]bool // tool names hidden from the device display

	// usage holds the latest per-tool subscription-usage snapshots posted to
	// POST /v1/usage. In-memory only; refreshed on a <=5-min cadence.
	usage *UsageStore

	// weather holds the latest fetched observation + popup bookkeeping; the
	// poller (StartWeather) writes it and the coordinator reads it for the tile.
	// weatherFetcher performs the provider HTTP calls. Both non-nil from NewApp.
	weather        *weatherStore
	weatherFetcher *weatherFetcher

	// meetingsURLs holds the ICS calendar feed URLs parsed from
	// EMBER_MEETINGS_ICS_URLS at startup. These are credentials (possession =
	// calendar read access) and are never serialised to JSON, logged as strings,
	// or stored; only the count is exposed via the config GET endpoint.
	meetingsURLs []string

	// meetings holds upcoming occurrences + popup bookkeeping; the poller
	// (StartMeetings) writes it and the coordinator reads it for the tile.
	// meetingsFetcher performs the ICS HTTP calls. Both non-nil from NewApp.
	meetings        *meetingsStore
	meetingsFetcher *icsFetcher

	// iconFetch downloads a LaMetric gallery icon by ID for the native icon
	// provisioner (ensureNativeIcons); injectable in tests. iconMu serialises
	// provisioner runs.
	iconFetch func(ctx context.Context, id string) (data []byte, ext string, err error)
	iconMu    sync.Mutex

	// deviceBaseline is the clock URL from config.json (captured at boot and
	// updated when /admin/reload applies a changed file URL), before any store
	// override or auto-discovery. deviceSource() uses it to tell "config" from
	// "discovered". Guarded by cfgMu. browseFn is the mDNS browse, overridable in tests.
	deviceBaseline   string
	deviceAutoPicked atomic.Bool // set by rediscoverClock (boot or watch goroutine) when discovery chose the clock URL
	republish        republishGate
	browseFn         func(context.Context, time.Duration) ([]discovery.Candidate, error)

	// deviceRediscoverMu single-flights rediscoverClock so the boot check and
	// the periodic probe can't browse mDNS concurrently. lastRediscoverAt /
	// lastRediscoverResult record the most recent attempt for /admin/doctor.
	deviceRediscoverMu   sync.Mutex
	lastRediscoverAt     atomic.Int64 // unix secs, 0 = never
	lastRediscoverResult atomic.Value // string: "reachable" | "swapped" | "no-device"

	// caps caches GET /api/v1/capabilities (the firmware's supported effect /
	// transition / overlay / palette names), refreshed at startup and on
	// rediscovery; deviceVersion is the clock's firmware version from the same
	// refresh. See device_capabilities.go.
	caps          atomic.Pointer[awtrix.Capabilities]
	deviceVersion atomic.Value // string

	// lastButtonAt is the unix-seconds time of the most recent device button
	// POST to /hooks/awtrix/button (0 = never). Proves the clock's button_callback
	// reaches us; surfaced via GET /v1/device/buttons.
	lastButtonAt atomic.Int64

	// Dashboard read state (dashboard_http.go, clock_health_http.go), all
	// zero-value ready: clockProbe caches the clock telemetry so the open
	// health endpoint can't turn polling into clock traffic; publishWindow keeps
	// 24h publish counts; firmware caches the latest awtrix-ng release (url set
	// by main); sourceColors remembers each producer source's colour.
	clockProbe    clockProbeCache
	publishWindow publishWindow
	firmware      firmwareCheck
	sourceColors  sourceColorMemo

	// bootPingMu serialises ensureBootPingScript runs (startup and every
	// /admin/reload), so two of them can't race a PUT against a DELETE.
	bootPingMu sync.Mutex
}

// NewApp builds the App. A nil publisher means the real clock (the App's own
// clockAccess); tests pass a fake Publisher instead.
func NewApp(cfg Config, publisher Publisher, logger *slog.Logger) *App {
	a := &App{
		publisher:      publisher,
		logger:         logger,
		versionInfo:    computeVersionInfo(),
		startedAt:      time.Now(),
		usage:          newUsageStore(),
		weather:        newWeatherStore(),
		activityLast:   make(map[string]activityMark),
		deviceBaseline: cfg.AWTRIX.HTTPBaseURL,
		browseFn:       discovery.BrowseAWTRIX,
	}
	a.weatherFetcher = newWeatherFetcher()
	a.meetings = newMeetingsStore()
	a.meetingsFetcher = newICSFetcher()
	a.iconFetch = fetchLaMetricIcon
	a.cfg.Store(&cfg)
	a.clock = newClockAccess(a.cfg.Load)
	a.sessions = a.newSessionRegistry(realClock{}.Now)
	a.settings = newAppSettings(a)
	a.metrics = newMetrics()
	a.limiter = NewIPLimiter(a)
	if publisher == nil {
		publisher = clockPublisher{a.clock}
	}
	// Every server-initiated write, and so every sound, goes through the
	// quiet-hours gate: the ungated publisher is never stored or handed out.
	// a.clock (menu proxy, probes) has no Publisher methods of its own.
	quiet := &quietPublisher{Publisher: publisher, cfg: a.cfg.Load, now: time.Now}
	a.publisher = quiet
	a.coord = newCoordinator(cfg, a.cfg.Load, quiet, realClock{}, logger, a.metrics)
	a.coord.snapshot = a.Snapshot
	a.coord.onPublishResult = a.recordPublish
	a.hiddenApps = map[string]bool{}
	a.coord.hiddenApps = a.hiddenAppsSet
	a.coord.usage = a.usage
	a.coord.weather = a.weather
	a.coord.meetings = a.meetings
	return a
}

// updateConfig serializes a config read-copy-write: it locks cfgMu, loads
// the current config, lets mutate apply changes to a copy, and stores the
// result. This closes the lost-update window that a bare
// `cur := *a.cfg.Load(); cur.X = ...; a.cfg.Store(&cur)` leaves open when two
// settings appliers race — the loser's stale copy would silently revert the
// winner's change. Readers stay lock-free via cfg.Load() (unchanged).
func (a *App) updateConfig(mutate func(*Config)) {
	_ = a.tryUpdateConfig(func(c *Config) error { mutate(c); return nil })
}

// tryUpdateConfig is updateConfig for a mutation that can fail: when mutate
// returns an error the copy is discarded and the live config is untouched.
// Validation therefore sees the same snapshot it merges onto (settings
// overlay), with no window for another writer in between.
func (a *App) tryUpdateConfig(mutate func(*Config) error) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cur := *a.cfg.Load()
	if err := mutate(&cur); err != nil {
		return err
	}
	a.cfg.Store(&cur)
	return nil
}

// recordPublish updates the last-publish telemetry + lastPublished
// metadata exposed to the admin endpoints. Called by the coordinator
// after every publish attempt; guarded by App.mu.
//
// The snap argument carries the legacy Render struct (legacyRender's
// text/color/counter output) so admin tooling can show what was last
// pushed even though the actual pixels are now produced by
// RenderForCoord and not stored anywhere.
func (a *App) recordPublish(snap Snapshot, err error) {
	now := time.Now()
	a.publishWindow.add(now, err == nil)
	a.mu.Lock()
	a.lastPublishAt = now.UTC()
	a.lastPublishOK = err == nil
	if err != nil {
		a.lastPublishErr = err.Error()
	} else {
		a.lastPublishErr = ""
		a.lastPublished = snap.Render
	}
	a.mu.Unlock()
}

// ClearIndicators turns off all three right-side indicator LEDs. Called
// once at server startup as part of the G.1a retirement of the old
// per-frame indicator semantics. Failures are not fatal (the device may
// be temporarily unreachable); the caller logs and continues.
// Subsequent Publish calls do not touch the indicators.
func (a *App) ClearIndicators(ctx context.Context) error {
	for i := 1; i <= 3; i++ {
		if err := a.publisher.ClearIndicator(ctx, i); err != nil {
			return fmt.Errorf("clear indicator %d: %w", i, err)
		}
	}
	return nil
}

// StartCoordinator runs the display coordinator goroutine + a dwell
// ticker that sends cmdTick on each interval. Blocks until ctx is done and
// the coordinator has finished its exit cleanup; the Pomodoro ticker runs on
// this goroutine, so no pomoTick store write happens after it returns.
func (a *App) StartCoordinator(ctx context.Context) {
	cfg := a.cfg.Load()
	dwell := time.Duration(cfg.Display.RotationDwellSeconds) * time.Second
	if dwell <= 0 {
		dwell = 3 * time.Second
	}

	// Run's deferred takeover restore talks to the clock after ctx is
	// cancelled; returning only once it's done lets main wait for it.
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		a.coord.Run(ctx)
	}()
	defer func() { <-runDone }()

	ticker := time.NewTicker(dwell)
	defer ticker.Stop()

	// While the Pomodoro feature is enabled, a 1 s ticker advances the engine
	// and refreshes the countdown. pomoTick is a cheap no-op when the engine
	// is idle, so the ticker runs unconditionally when the feature is wired.
	var pomoC <-chan time.Time
	if a.engine != nil {
		pt := time.NewTicker(time.Second)
		defer pt.Stop()
		pomoC = pt.C
	}

	// Emit an initial tick so the first frame appears right after startup.
	a.coord.Send(coordCmd{kind: cmdTick})
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			retuneDwellTicker(ticker, &dwell, a.cfg.Load())
			a.coord.Send(coordCmd{kind: cmdTick})
		case <-pomoC:
			a.pomoTick()
		}
	}
}
