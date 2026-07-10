package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const httpTimeout = 5 * time.Second

// daemonFailLog throttles POST/DELETE failure warnings across ticks (~1/min
// per failure kind), so a stalled server doesn't flood the log every
// pollInterval.
var daemonFailLog = producer.NewFailureLogger(time.Minute)

// runDaemon is the entry point for `ember-codex-producer run` (and the bare
// default). It polls until SIGINT/SIGTERM.
func runDaemon() {
	rotateCodexLogs()
	openDaemonLogOrStderr("ember-codex-producer")
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		fmt.Fprintln(os.Stderr, "codex producer: EMBER_SOURCE/EMBER_SERVER_URL not set; nothing to do")
		os.Exit(0)
	}
	w := newWatcher(cfg)
	client := producer.NewClient(cfg.ServerURL, cfg.Token, httpTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		runOnce(ctx, w, client)
		select {
		case <-ctx.Done():
			for _, ss := range w.sessions {
				_ = removeMarker(cfg.StateDir, ss.uuid)
			}
			return
		case <-ticker.C:
		}
	}
}

// openDaemonLogOrStderr routes stdout/stderr and the slog default logger to
// ~/Library/Logs/<name>.log so the daemon logs correctly even when launched
// from a plist with no StandardOutPath (a bundled static plist can't encode a
// per-user path). Best-effort: on failure it silently leaves stderr as-is.
func openDaemonLogOrStderr(name string) {
	f, err := producer.OpenDaemonLog(name)
	if err != nil {
		return
	}
	os.Stdout = f
	os.Stderr = f
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

// runOnce performs a single reconcile pass, issuing POSTs/DELETEs and keeping
// the menu-bar marker files in sync (write on POST, remove on DELETE).
func runOnce(ctx context.Context, w *watcher, client *producer.Client) {
	posts, deletes, usages := w.tick()
	for _, req := range posts {
		if err := client.Post(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_post", "status POST failed", "err", err)
		}
		if body, err := json.Marshal(req); err == nil {
			_ = writeMarker(w.cfg.StateDir, req.Session, body)
		}
	}
	for _, req := range deletes {
		if err := client.Delete(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_delete", "status DELETE failed", "err", err)
		}
		_ = removeMarker(w.cfg.StateDir, req.Session)
	}
	for _, u := range usages {
		if err := client.Usage(ctx, u); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_usage", "usage POST failed", "err", err)
		}
	}
}

func rotateCodexLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	producer.RotateLogIfLarge(filepath.Join(home, "Library", "Logs", "ember-codex-producer.log"), producer.DefaultLogThreshold)
}
