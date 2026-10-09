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
	viewLimit    *callerLimiter

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

	reminderHeldUntil atomic.Int64
	reminderLoop      reminderLoop

	reminderKeys reminderDedupe

	activityMu      sync.Mutex
	activityLast    map[string]activityMark
	activitySweptAt time.Time

	brightness brightnessTracker

	statsCache statsCache

	settings appSettings

	devices       *deviceRegistry
	clockSync     clockSyncGate
	clockRotation atomic.Pointer[clockRotation]
	rotationOp    clockRotationOp
	commitHook    func()
	clockMigrate  clockMigration
	clients       *clientRegistry
	changes       *changeBroadcaster
	viewWaiters   viewWaiters
	viewRecheck   time.Duration
	viewClock     func() time.Time
	viewWaitHook  func()
	otaReadHook   func()
	knobStats     *knobStatsStore
	wifiDrops     checkinDropLog
	diagDrops     checkinDropLog
	coredumps     *coredumpStore
	knobFW        *firmwareStore
	ota           otaLive
	otaDrops      checkinDropLog
	capsDrops     checkinDropLog
	viewOverLimit checkinDropLog
	firmwareLimit *callerLimiter
	clockStats    *clockStatsStore

	appsMu     sync.Mutex
	hiddenApps map[string]bool

	usage *UsageStore

	weather        *weatherStore
	weatherFetcher *weatherFetcher

	meetingsURLs []string

	nowPlaying *nowPlayingService

	meetings        *meetingsStore
	meetingsFetcher *icsFetcher

	iconFetch func(ctx context.Context, id string) (data []byte, ext string, err error)
	iconMu    sync.Mutex
	iconJobs  sync.WaitGroup

	republish republishGate
	browseFn  func(context.Context, time.Duration) ([]discovery.Candidate, error)

	deviceRediscoverMu   sync.Mutex
	lastRediscoverAt     atomic.Int64
	lastRediscoverResult atomic.Value

	caps          atomic.Pointer[awtrix.Capabilities]
	deviceVersion atomic.Value

	lastButtonAt atomic.Int64

	clockProbe    clockProbeCache
	publishWindow publishWindow
	firmware      firmwareCheck
	sourceColors  sourceColorMemo

	bootPingMu sync.Mutex
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
		browseFn:     discovery.BrowseAWTRIX,
	}
	a.weatherFetcher = newWeatherFetcher()
	a.meetings = newMeetingsStore()
	a.meetingsFetcher = newICSFetcher()
	a.nowPlaying = newNowPlayingService()
	a.nowPlaying.reg.OnChange = func() { a.changes.notify(topicNowPlaying) }
	a.iconFetch = fetchLaMetricIcon
	a.cfg.Store(&cfg)
	a.changes = newChangeBroadcaster()
	a.clock = newClockAccess(a.cfg.Load)
	a.rotationOp.slot = make(chan struct{}, 1)
	a.rotationOp.budget.Store(int64(clockRotationOpBudget))
	a.sessions = a.newSessionRegistry(realClock{}.Now)
	a.settings = newAppSettings(a)
	a.devices = newDeviceRegistry(func() settingsKV {
		if a.store == nil {
			return nil
		}
		return a.store
	})
	a.devices.onChange = func() { a.changes.notify(topicDevices) }
	a.clients = newClientRegistry(a.devices.kv)
	a.knobStats = newKnobStatsStore()
	a.clockStats = newClockStatsStore()
	a.metrics = newMetrics()
	a.limiter = NewIPLimiter(a)
	a.viewLimit = &callerLimiter{burst: viewBurst, perSec: viewPerSec}
	a.firmwareLimit = &callerLimiter{burst: firmwareDownloadBurst, perSec: firmwareDownloadPerSec}
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
	a.coord.onPublishResult = a.recordPublish
	a.hiddenApps = map[string]bool{}
	a.coord.hiddenApps = a.hiddenAppsSet
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
