package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	// Embedded tz database: TZID resolution must work in the distroless container
	// image that ships no zoneinfo files; the meetings ICS parser uses LoadLocation.
	_ "time/tzdata"

	"github.com/tarakanof/ember/internal/discovery"
)

func main() {
	if sub, args, ok := scanSubcommand(os.Args[1:]); ok {
		switch sub {
		case "version", "-v", "--version":
			runVersion()
			return
		case "healthcheck":
			runHealthcheck()
			return
		case "doctor":
			runDoctor(args)
			return
		}
	}

	configFlag := flag.String("config", "", "path to config JSON file")
	printConfig := flag.Bool("print-config", false, "print loaded config (post-defaults, secrets redacted) to stdout and exit")
	flag.Parse()

	if *printConfig {
		runPrintConfig(*configFlag)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	configPath, configSource := resolveConfigPath(*configFlag)
	cfg, err := loadConfig(*configFlag, logger)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	tlsCfg, err := readTLSEnv()
	if err != nil {
		logger.Error("TLS configuration invalid", "err", err)
		os.Exit(1)
	}

	publisher, err := NewHTTPPublisher()
	if err != nil {
		logger.Error("create publisher failed", "err", err)
		os.Exit(1)
	}

	app := NewApp(cfg, publisher, logger)
	// The dashboard's "update available" badge looks up the latest awtrix-ng
	// release on GitHub; EMBER_FIRMWARE_CHECK=0 keeps the server offline.
	if envEnabled(os.Getenv("EMBER_FIRMWARE_CHECK")) {
		app.firmware.url = ngReleasesURL
	}
	app.configPath = configPath
	app.configSource = configSource

	// Always wire the Pomodoro engine so the feature can be toggled at runtime
	// from the app (cfg.Pomodoro.Enabled — persisted to the store — gates whether
	// it runs). Non-fatal: if the store can't open, the feature is simply
	// unavailable until the data dir is writable.
	if err := app.initPomodoro(cfg.Pomodoro); err != nil {
		logger.Warn("pomodoro init failed; feature unavailable until the data store is writable", "err", err, "db_path", cfg.Pomodoro.DBPath)
	} else {
		logger.Info("pomodoro wired", "enabled", app.cfg.Load().Pomodoro.Enabled, "db_path", cfg.Pomodoro.DBPath, "button_callback", cfg.Pomodoro.ButtonCallback)
	}
	// ICS calendar URLs are credentials; they live only in the env var and are
	// never logged as strings, stored, or echoed in API responses (count only).
	{
		var meetingsDropped int
		app.meetingsURLs, meetingsDropped = parseICSURLs(os.Getenv("EMBER_MEETINGS_ICS_URLS"))
		if meetingsDropped > 0 {
			logger.Warn("meetings ICS feed entries ignored (unsupported scheme)", "dropped", meetingsDropped)
		}
	}
	if len(app.meetingsURLs) > 0 {
		logger.Info("meetings ICS feeds configured", "count", len(app.meetingsURLs))
	}
	app.settings.reapply() // runtime settings overrides over the file baseline
	if cfg.Weather.Enabled {
		logger.Info("weather enabled", "provider", cfg.Weather.Provider, "location", cfg.Weather.LocationName)
	}
	if cfg.Meetings.IsEnabled() && len(app.meetingsURLs) > 0 {
		logger.Info("meetings enabled", "ics_feeds", len(app.meetingsURLs))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Resolve the clock address before the coordinator publishes: store override
	// > reachable config.json baseline > mDNS auto-discovery. Bounded so it can't
	// stall startup for long; a no-op when a reachable URL is already configured.
	app.initDeviceDiscovery(ctx)

	// Every background worker joins workers so shutdown can wait for their
	// cleanup (the coordinator's takeover restore) before closing the store.
	var workers sync.WaitGroup

	// Advertise the server over mDNS so the macOS app can discover it (requires
	// host/macvlan networking to reach the LAN). Non-fatal; off via
	// EMBER_MDNS_ADVERTISE=0.
	if envEnabled(os.Getenv("EMBER_MDNS_ADVERTISE")) {
		if port, perr := discovery.PortFromAddr(cfg.HTTP.Addr); perr == nil {
			ver := app.versionInfo.Revision
			if ver == "" {
				ver = "dev"
			}
			logger.Info("mDNS advertising enabled", "service", "_ember._tcp", "port", port)
			workers.Go(func() {
				if err := discovery.Advertise(ctx, "Ember", port, ver); err != nil && ctx.Err() == nil {
					logger.Warn("mDNS advertise stopped", "err", err)
				}
			})
		} else {
			logger.Warn("mDNS advertise skipped: cannot parse port", "addr", cfg.HTTP.Addr, "err", perr)
		}
	} else {
		logger.Info("mDNS advertising disabled (EMBER_MDNS_ADVERTISE)")
	}

	if err := app.ClearIndicators(context.Background()); err != nil {
		logger.Warn("clear indicators on startup failed", "err", err)
	}

	workers.Go(func() { app.limiter.runSweeper(ctx) })
	workers.Go(func() { app.StartCoordinator(ctx) })
	workers.Go(func() { app.StartWeather(ctx) })
	workers.Go(func() { app.StartMeetings(ctx) })
	workers.Go(func() { app.StartReminderLoopGuard(ctx) })
	// Off the startup path: it does device HTTP, and a clock that isn't up yet
	// must not delay the listener. Re-run after every /admin/reload.
	workers.Go(func() { app.ensureBootPingScript(ctx) })

	// Periodic self-healing watch: re-check the effective clock URL and swap to
	// a reachable mDNS candidate if it's gone dark, and re-push everything when
	// the clock's uptime shows it rebooted. Off via awtrix.auto_rediscover=false.
	if cfg.AWTRIX.AutoRediscoverEnabled() {
		logger.Info("clock auto-rediscover enabled", "interval", deviceWatchInterval.String())
		workers.Go(func() { app.StartDeviceWatch(ctx, deviceWatchInterval) })
	} else {
		logger.Info("clock auto-rediscover disabled (awtrix.auto_rediscover)")
	}

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	listener, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		logger.Error("listen failed", "err", err, "addr", cfg.HTTP.Addr)
		os.Exit(1)
	}
	app.listener = listener

	go func() {
		addr := listener.Addr().String()
		var serveErr error
		if tlsCfg.enabled {
			// Wrap the existing TCP listener so app.listener still points at
			// the raw socket for doctor's http_listening detail.
			tlsListener := tls.NewListener(listener, &tls.Config{
				Certificates: []tls.Certificate{tlsCfg.cert},
				MinVersion:   tls.VersionTLS12,
			})
			logger.Info("server listening (https)", "addr", addr)
			serveErr = server.Serve(tlsListener)
		} else {
			logger.Info("server listening", "addr", addr)
			serveErr = server.Serve(listener)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("server failed", "err", serveErr)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	app.shutdown(shutdownCtx, server, &workers)
}

// shutdownTimeout bounds the whole exit: HTTP drain plus the background
// workers' own cleanup, chiefly the coordinator's Pomodoro takeover restore
// (exitRestoreBudget). It stays under Docker's default 10 s stop grace period
// so the container is never SIGKILLed mid-restore.
const shutdownTimeout = 8 * time.Second

// shutdown stops the HTTP server, waits (until ctx is done) for the background
// workers, whose context the caller has already cancelled, and only then
// closes the store. Returning from main kills every goroutine on the spot, so
// without the wait the coordinator's exit restore PATCH never reaches the
// clock, and a last pomoTick can write to a closed DB.
func (a *App) shutdown(ctx context.Context, server *http.Server, workers *sync.WaitGroup) {
	if err := server.Shutdown(ctx); err != nil {
		a.logger.Warn("server shutdown failed", "err", err)
	}
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		a.logger.Warn("background workers still running at the shutdown deadline; closing the store anyway")
	}
	// Close the store so WAL is checkpointed and in-flight writes are flushed
	// before exit.
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.logger.Warn("pomodoro store close failed", "err", err)
		}
	}
}

// scanSubcommand walks args to find the first non-flag positional. It
// recognises `-flag=value` (single token) and `-flag value` (two tokens)
// for the server's own flags. Returns (token, remaining-after-token, true)
// if the token matches a known subcommand; otherwise ("", nil, false).
func scanSubcommand(args []string) (sub string, rest []string, ok bool) {
	known := map[string]bool{
		"version": true, "-v": true, "--version": true,
		"healthcheck": true,
		"doctor":      true,
	}
	flagWithValue := map[string]bool{
		"-config": true, "--config": true,
	}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if strings.Contains(tok, "=") && (strings.HasPrefix(tok, "-config=") || strings.HasPrefix(tok, "--config=")) {
			continue
		}
		if flagWithValue[tok] {
			i++ // skip its value
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue // unknown flag; skip
		}
		if known[tok] {
			return tok, args[i+1:], true
		}
		return "", nil, false
	}
	return "", nil, false
}
