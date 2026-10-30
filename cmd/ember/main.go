package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/nowplaying"
	"github.com/tarakanof/ember/internal/weather"
)

func main() {
	sub, args, ok, err := scanSubcommand(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ember:", err)
		os.Exit(2)
	}
	if ok {
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
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "ember: unexpected argument %q (subcommands: version, healthcheck, doctor)\n", flag.Arg(0))
		os.Exit(2)
	}

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

	app := newServerApp(cfg, logger)
	app.weatherFetcher.TomorrowKey = strings.TrimSpace(os.Getenv(weather.TomorrowKeyEnv))
	app.weatherFetcher.WeatherAPIKey = strings.TrimSpace(os.Getenv(weather.WeatherAPIKeyEnv))
	for _, p := range weather.Providers {
		if k, ok := weather.KeyedProviders[p]; ok && app.weatherFetcher.Key(p) != "" {
			logger.Info("weather provider available", "provider", p, "key_env", k.Env)
		}
	}
	if envEnabled(os.Getenv("EMBER_FIRMWARE_CHECK")) && !clockDisabled() {
		app.firmware.url = ngReleasesURL
	}
	app.configPath = configPath
	app.configSource = configSource

	pomoErr := app.initPomodoro(cfg.Pomodoro)
	releaseRotation := app.holdClockRotation()
	app.reapplySettings()
	releaseRotation()
	app.migrateClockConfig()
	if w := app.cfg.Load().Weather; w.Enabled {
		if reason := app.weatherUnavailableReason(w); reason != "" {
			logger.Warn("weather unavailable: no fetches until the key is set or another provider is chosen", "reason", reason)
		}
	}
	if pomoErr != nil {
		logger.Warn("pomodoro init failed; feature unavailable and settings will not persist until the data store is writable", "err", pomoErr, "db_path", cfg.Pomodoro.DBPath)
	} else {
		logger.Info("pomodoro wired", "enabled", app.cfg.Load().Pomodoro.Enabled, "db_path", cfg.Pomodoro.DBPath, "button_callback", cfg.Pomodoro.ButtonCallback)
	}
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
	if plexCfg, ok := nowplaying.PlexConfigFromEnv(os.Getenv); ok {
		app.nowPlaying.Plex = nowplaying.NewPlexSource(plexCfg)
		logger.Info("plex now-playing source enabled", "plex", plexCfg)
	} else if plexCfg.URL != "" || plexCfg.Token != "" {
		logger.Warn("plex now-playing source disabled: EMBER_PLEX_URL must be http(s) and EMBER_PLEX_TOKEN set")
	}
	if envOptIn(os.Getenv("EMBER_ARTIST_LOOKUP")) {
		app.nowPlaying.Artists = nowplaying.NewArtistLookup(deezerAPIBase)
		logger.Info("artist pictures: Deezer lookup on (artist names leave the server; unset EMBER_ARTIST_LOOKUP to stop)")
	}
	if cfg.Weather.Enabled {
		logger.Info("weather enabled", "provider", cfg.Weather.Provider, "model", cfg.Weather.Model(), "location", cfg.Weather.LocationName)
	}
	if cfg.Meetings.IsEnabled() && len(app.meetingsURLs) > 0 {
		logger.Info("meetings enabled", "ics_feeds", len(app.meetingsURLs))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if clockDisabled() {
		logger.Warn("clock disabled (EMBER_CLOCK=off): no clock I/O, publishes are dropped")
	} else {
		app.initDeviceDiscovery(ctx)
	}

	var workers sync.WaitGroup

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

	if !clockDisabled() {
		clearCtx, cancel := context.WithTimeout(context.Background(), startupClearBudget)
		err := app.ClearIndicators(clearCtx)
		cancel()
		if err != nil {
			logger.Warn("clear indicators on startup failed", "err", err)
		}
	}

	workers.Go(func() { app.limiter.runSweeper(ctx) })
	workers.Go(func() { app.StartCoordinator(ctx) })
	workers.Go(func() { app.StartWeather(ctx) })
	workers.Go(func() { app.StartBrightness(ctx) })
	workers.Go(func() { app.StartMeetings(ctx) })
	workers.Go(func() { app.StartNowPlaying(ctx) })
	workers.Go(func() { app.StartReminderLoopGuard(ctx) })

	if clockDisabled() {
		logger.Info("clock watch and sampler off (EMBER_CLOCK=off)")
	} else if cfg.AWTRIX.AutoRediscoverEnabled() {
		logger.Info("clock auto-rediscover enabled", "interval", deviceWatchInterval.String())
		workers.Go(func() { app.StartDeviceWatch(ctx, deviceWatchInterval) })
	} else {
		logger.Info("clock auto-rediscover disabled (awtrix.auto_rediscover)")
		workers.Go(func() { app.StartClockSampler(ctx, clockProbeTTL) })
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

const shutdownTimeout = 8 * time.Second

func (a *App) shutdown(ctx context.Context, server *http.Server, workers *sync.WaitGroup) {
	a.changes.close()
	a.clockJobs.close()
	if err := server.Shutdown(ctx); err != nil {
		a.logger.Warn("server shutdown failed", "err", err)
	}
	done := make(chan struct{})
	go func() {
		a.clockMigrate.stopAndWait()
		workers.Wait()
		a.clockJobs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		a.logger.Warn("background workers still running at the shutdown deadline; closing the store anyway")
	}
	if err := a.devices.Flush(); err != nil {
		a.logger.Warn("device registry flush failed", "err", err)
	}
	if err := a.usageSeen.flush(); err != nil {
		a.logger.Warn("usage account registry flush failed", "err", err)
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.logger.Warn("pomodoro store close failed", "err", err)
		}
	}
}

func scanSubcommand(args []string) (sub string, rest []string, ok bool, err error) {
	known := map[string]bool{
		"version":     true,
		"healthcheck": true,
		"doctor":      true,
	}
	flagWithValue := map[string]bool{
		"-config": true, "--config": true,
	}
	var global, stray, versionRest []string
	versionTok := ""
	versionResult := func() (string, []string, bool, error) {
		if versionTok == "" {
			return "", nil, false, nil
		}
		return versionTok, versionRest, true, nil
	}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "-v" || tok == "--version" {
			if versionTok == "" {
				versionTok = tok
				versionRest = append(slices.Clone(global), args[i+1:]...)
			}
			continue
		}
		if tok == "version" && versionTok != "" {
			return versionResult()
		}
		if known[tok] {
			if versionTok != "" {
				return "", nil, false, strayFlagError([]string{versionTok}, tok)
			}
			if stray != nil {
				return "", nil, false, strayFlagError(stray, tok)
			}
			return tok, append(global, args[i+1:]...), true, nil
		}
		if tok == "--" {
			return versionResult()
		}
		if strings.HasPrefix(tok, "-config=") || strings.HasPrefix(tok, "--config=") {
			global = append(global, tok)
			continue
		}
		if flagWithValue[tok] {
			global = append(global, args[i:min(i+2, len(args))]...)
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			flagToks := []string{tok}
			if doctorFlagTakesValue(tok) && i+1 < len(args) {
				flagToks = append(flagToks, args[i+1])
				i++
			}
			if stray == nil {
				stray = flagToks
			}
			continue
		}
		return versionResult()
	}
	return versionResult()
}

func doctorFlagTakesValue(tok string) bool {
	if strings.Contains(tok, "=") {
		return false
	}
	f := newDoctorFlags().fs.Lookup(strings.TrimLeft(tok, "-"))
	if f == nil {
		return false
	}
	b, isBool := f.Value.(interface{ IsBoolFlag() bool })
	return !isBool || !b.IsBoolFlag()
}

func strayFlagError(stray []string, sub string) error {
	flagTok := stray[0]
	prefix := fmt.Sprintf("flag %s before subcommand %s", flagTok, sub)
	name, _, _ := strings.Cut(strings.TrimLeft(flagTok, "-"), "=")
	switch {
	case flagTok == "-v" || flagTok == "--version":
		return fmt.Errorf("%s: %s prints the version and does not combine with a subcommand; run `ember %s` and `ember %s` separately", prefix, flagTok, flagTok, sub)
	case name == "print-config":
		return fmt.Errorf("%s: -print-config is a server flag and does not combine with a subcommand; run `ember -print-config` and `ember %s` separately", prefix, sub)
	case (name == "h" || name == "help") && sub == "doctor":
		return fmt.Errorf("%s: for doctor's flags run `ember doctor -h`, for the server's `ember -h`", prefix)
	case name == "h" || name == "help":
		return fmt.Errorf("%s: for the server's flags run `ember -h`", prefix)
	case sub == "doctor" && newDoctorFlags().fs.Lookup(name) != nil:
		return fmt.Errorf("%s: put doctor flags after it (`ember doctor %s`)", prefix, strings.Join(stray, " "))
	case sub == "doctor":
		return fmt.Errorf("%s: %s is not a doctor flag; see `ember doctor -h`", prefix, flagTok)
	}
	return fmt.Errorf("%s: %s takes no flags", prefix, sub)
}
