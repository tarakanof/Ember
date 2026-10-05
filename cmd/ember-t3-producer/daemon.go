package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	httpTimeout = 5 * time.Second
	maxBackoff  = time.Minute
)

// pinnedMigrations is the newest effect_sql_migrations id verified per schema:
// v1 = T3 Code v0.0.45, v2 = T3 Code v0.0.46-preview.20261002.2598
// (T3 main and the 0.0.46 nightlies through 2026-10-05 still end at 56). A newer id
// is logged once and still read; a missing table or column is a soft failure.
var pinnedMigrations = map[int]int{1: 54, 2: 56}

var daemonFailLog = producer.NewFailureLogger(time.Minute)

type daemon struct {
	cfg     Config
	client  *producer.Client
	watcher *watcher
	now     func() time.Time
	alive   func(home string) bool
	warned  map[[2]int]bool
	store   *storeReader
}

func newDaemon(cfg Config, client *producer.Client) *daemon {
	return &daemon{
		cfg:     cfg,
		client:  client,
		watcher: newWatcher(cfg),
		now:     time.Now,
		alive:   func(home string) bool { return serverAlive(home, pidAlive) },
		warned:  map[[2]int]bool{},
		store:   &storeReader{},
	}
}

func runDaemon() {
	producer.StartDaemonLog("ember-t3-producer")
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || (cfg.ServerURL == "" && !cfg.ServerAuto) {
		fmt.Fprintln(os.Stderr, "t3 producer: EMBER_SOURCE/EMBER_SERVER_URL not set; nothing to do")
		os.Exit(0)
	}
	slog.Info("t3 producer starting", "config", cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	auto, ok := producer.DaemonServer(ctx, home, cfg.ServerURL, cfg.ServerAuto, cfg.ServerInstance)
	if !ok {
		return
	}
	client := producer.NewClient(cfg.ServerURL, cfg.Token, httpTimeout).WithAutoServer(auto)
	if path, err := producer.LinkStatusPath("t3-producer"); err == nil {
		client.WithLinkStatus(producer.NewLinkStatus(path))
	}
	sweepMarkers(cfg.StateDir)
	d := newDaemon(cfg, client)
	defer d.store.Close()

	base := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	failures := 0
	for {
		if err := d.poll(ctx); err != nil {
			failures++
			daemonFailLog.Warn(slog.Default(), "t3_read", "reading T3 state failed; backing off", "err", err, "retry_in", backoff(base, failures))
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			// Best effort on shutdown: a fresh context, since ctx is already cancelled.
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			d.send(shutdown, nil, d.watcher.dropAll())
			cancel()
			return
		case <-time.After(backoff(base, failures)):
		}
	}
}

// poll reads T3 once and reports the difference. A read error leaves the
// reported threads alone (the server reaps them if it lasts) and is returned
// so the loop can back off.
func (d *daemon) poll(ctx context.Context) error {
	if !d.alive(d.cfg.T3Home) {
		d.send(ctx, nil, d.watcher.dropAll())
		return nil
	}
	snap, err := d.store.Read(ctx, d.cfg.T3Home)
	if err != nil {
		return err
	}
	if d.noteMigration(snap.Schema, snap.Migration) {
		slog.Warn("T3 Code schema is newer than the verified one; reading it anyway",
			"schema", snap.Schema, "migration", snap.Migration, "verified", pinnedMigrations[snap.Schema])
	}
	posts, deletes := d.watcher.tick(snap.Threads, d.now())
	d.send(ctx, posts, deletes)
	return nil
}

// noteMigration reports whether migration is newer than the pinned one for
// schema and has not been reported yet.
func (d *daemon) noteMigration(schema, migration int) bool {
	if migration <= pinnedMigrations[schema] || d.warned[[2]int{schema, migration}] {
		return false
	}
	d.warned[[2]int{schema, migration}] = true
	return true
}

func (d *daemon) send(ctx context.Context, posts []producer.StatusRequest, deletes []producer.DeleteRequest) {
	for _, req := range posts {
		if err := d.client.Post(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "t3_post", "status POST failed", "err", err)
		}
		if body, err := json.Marshal(req); err == nil {
			_ = writeMarker(d.cfg.StateDir, req.Session, body)
		}
	}
	for _, req := range deletes {
		if err := d.client.Delete(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "t3_delete", "status DELETE failed", "err", err)
		}
		_ = removeMarker(d.cfg.StateDir, req.Session)
	}
}

// backoff doubles base per consecutive failure, capped at maxBackoff.
func backoff(base time.Duration, failures int) time.Duration {
	d := base
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
