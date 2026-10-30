package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/nowplaying"
	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/sessions"
	"github.com/tarakanof/ember/internal/weather"
)

type App struct {
	cfg          atomic.Pointer[Config]
	cfgMu        sync.Mutex
	configPath   string
	configSource string
	publisher    Publisher
	clock        *clockAccess
	logger       *slog.Logger
	listener     net.Listener
	versionInfo  versionInfo
	startedAt    time.Time
	limiter      *IPLimiter
	viewLimit    *nowplaying.CallerLimiter

	sessions *sessions.Registry

	mu            sync.Mutex
	lastPublished Render

	lastPublishAt  time.Time
	lastPublishOK  bool
	lastPublishErr string

	metrics *metrics
	coord   *coordinator

	engine *pomodoro.Engine
	store  *pomodoro.Store

	day      dayTracker
	dayClock func() time.Time

	reminderHeldUntil atomic.Int64
	reminderLoop      reminderLoop

	reminderKeys reminderDedupe

	activityMu      sync.Mutex
	activityLast    map[string]activityMark
	activitySweptAt time.Time

	brightness brightnessTracker

	statsCache statsCache
	statsClock func() time.Time
	zone       *time.Location

	settings appSettings

	devices       *devices.Registry
	clockSync     clockSyncGate
	clockRotation atomic.Pointer[clockRotation]
	rotationOp    clockRotationOp
	clockMigrate  clockMigration
	clients       *devices.ClientRegistry
	claims        *devices.ClaimRegistry
	changes       *changeBroadcaster
	viewWaiters   viewWaiters
	stateWaiters  viewWaiters
	demos         demoRegistry
	viewRecheck   time.Duration
	viewClock     func() time.Time
	watchedAfter  func(time.Duration) (<-chan time.Time, func())
	watchedRead   func()
	knobStats     *knobStatsStore
	wifiDrops     checkinDropLog
	diagDrops     checkinDropLog
	idleDrops     checkinDropLog
	roomDrops     checkinDropLog
	coredumps     *coredumpStore
	knobFW        *firmwareStore
	ota           otaLive
	otaDrops      checkinDropLog
	capsDrops     checkinDropLog
	viewOverLimit checkinDropLog
	knobShown     knobShownStore
	shownDrops    checkinDropLog
	firmwareLimit *nowplaying.CallerLimiter
	clockStats    *clockStatsStore

	commitHook        func()
	migrationHoldHook func()
	viewWaitHook      func()
	shownSetHook      func()
	otaReadHook       func()

	appsMu     sync.Mutex
	hiddenApps map[string]bool

	usage     *UsageStore
	usageSeen *usageAccountRegistry

	weather        *weatherStore
	weatherFetcher *weather.Fetcher

	meetingsURLs []string

	nowPlaying *nowplaying.Service
	controls   *controlsService

	meetings        *meetingsStore
	meetingsFetcher *icsFetcher

	iconFetch func(ctx context.Context, id string) (data []byte, ext string, err error)
	iconMu    sync.Mutex
	clockJobs *clockJobGroup
	iconHold  atomic.Int32
	waitIcons atomic.Bool
	waitArmMu sync.Mutex
	clockGen  uint64

	republish  republishGate
	browseFn   browseFunc
	localIPFor func(host string) string

	deviceRediscoverMu   sync.Mutex
	lastRediscoverAt     atomic.Int64
	lastRediscoverResult atomic.Value

	caps          capsCache
	settingsSound settingsSoundCache
	deviceVersion atomic.Value
	appEnabled    appEnabledRoute

	lastButtonAt atomic.Int64

	clockProbe    clockProbeCache
	publishWindow publishWindow
	firmware      firmwareCheck
	sourceColors  sourceColorMemo

	bootPingMu sync.Mutex
}

func newServerApp(cfg Config, logger *slog.Logger) *App {
	app := NewApp(cfg, nil, logger)
	app.browseFn = discovery.BrowseAWTRIX
	return app
}

func NewApp(cfg Config, publisher Publisher, logger *slog.Logger) *App {
	a := &App{
		publisher:    publisher,
		logger:       logger,
		versionInfo:  computeVersionInfo(),
		startedAt:    time.Now(),
		usage:        newUsageStore(),
		weather:      newWeatherStore(),
		activityLast: make(map[string]activityMark),
		browseFn:     browseNotWired,
		localIPFor:   outboundIP,
	}
	a.weatherFetcher = weather.NewFetcher()
	a.meetings = newMeetingsStore()
	a.meetingsFetcher = newICSFetcher()
	a.nowPlaying = nowplaying.NewService()
	a.controls = newControlsService()
	a.nowPlaying.Reg.OnChange = func() { a.changes.notify(topicNowPlaying) }
	a.iconFetch = fetchLaMetricIcon
	a.clockJobs = newClockJobGroup()
	a.cfg.Store(&cfg)
	a.changes = newChangeBroadcaster()
	a.clock = newClockAccess(a.cfg.Load)
	a.rotationOp.slot = make(chan struct{}, 1)
	a.rotationOp.budget.Store(int64(clockRotationOpBudget))
	a.sessions = a.newSessionRegistry(realClock{}.Now)
	a.settings = newAppSettings(a)
	a.devices = devices.New(func() devices.KV {
		if a.store == nil {
			return nil
		}
		return a.store
	})
	a.devices.OnChange = func() {
		a.changes.notify(topicDevices)
		a.knobShown.bump()
	}
	a.clients = devices.NewClientRegistry(a.devices.KV)
	a.claims = devices.NewClaimRegistry()
	a.claims.OnExpire = func(c devices.Claim) {
		a.logger.Info("device claim expired", "claim_id", c.ID, "hw_id", c.HwID, "ip", c.IP)
	}
	a.usageSeen = newUsageAccountRegistry(a.devices.KV)
	a.knobStats = newKnobStatsStore()
	a.clockStats = newClockStatsStore()
	a.metrics = newMetrics()
	a.limiter = NewIPLimiter(a)
	a.viewLimit = &nowplaying.CallerLimiter{Burst: viewBurst, PerSec: viewPerSec}
	a.firmwareLimit = &nowplaying.CallerLimiter{Burst: firmwareDownloadBurst, PerSec: firmwareDownloadPerSec}
	if publisher == nil {
		if clockDisabled() {
			publisher = disabledPublisher{}
		} else {
			publisher = clockPublisher{a.clock}
		}
	}
	a.publisher = publisher
	a.coord = newCoordinator(cfg, a.cfg.Load, publisher, realClock{}, logger, a.metrics)
	a.coord.snapshot = a.Snapshot
	a.coord.quiet = a.quietAt
	a.coord.onPublishResult = a.recordPublish
	a.hiddenApps = map[string]bool{}
	a.coord.hiddenApps = a.hiddenAppsSet
	a.coord.waitIcons = a.waitIcons.Load
	a.coord.soundObject = a.soundObjectAPI
	a.coord.refreshSound = a.refreshCapabilities
	a.coord.usage = a.usage
	a.coord.weather = a.weather
	a.coord.meetings = a.meetings
	return a
}

func (a *App) updateConfig(mutate func(*Config)) {
	_ = a.tryUpdateConfig(func(c *Config) error { mutate(c); return nil })
}

func (a *App) tryUpdateConfig(mutate func(*Config) error) error {
	a.cfgMu.Lock()
	cur := *a.cfg.Load()
	if err := mutate(&cur); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	a.cfg.Store(&cur)
	a.changes.notify(topicConfig)
	a.cfgMu.Unlock()
	a.syncClockConfigVersion()
	return nil
}

func (a *App) recordPublish(snap Snapshot, err error) {
	if clockDisabled() {
		return
	}
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

const startupClearBudget = 5 * time.Second

func (a *App) ClearIndicators(ctx context.Context) error {
	for i := 1; i <= 3; i++ {
		if err := a.publisher.ClearIndicator(ctx, i); err != nil {
			return fmt.Errorf("clear indicator %d: %w", i, err)
		}
	}
	return nil
}

func (a *App) StartCoordinator(ctx context.Context) {
	cfg := a.cfg.Load()
	dwell := time.Duration(cfg.Display.RotationDwellSeconds) * time.Second
	if dwell <= 0 {
		dwell = 3 * time.Second
	}

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		a.coord.Run(ctx)
	}()
	defer func() { <-runDone }()

	ticker := time.NewTicker(dwell)
	defer ticker.Stop()

	var pomoC <-chan time.Time
	if a.engine != nil {
		pt := time.NewTicker(time.Second)
		defer pt.Stop()
		pomoC = pt.C
	}

	var dayC <-chan time.Time
	if a.store != nil {
		dt := time.NewTicker(dayTickInterval)
		defer dt.Stop()
		dayC = dt.C
		a.dayTick(a.dayNow())
	}

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
		case <-dayC:
			a.dayTick(a.dayNow())
		}
	}
}
