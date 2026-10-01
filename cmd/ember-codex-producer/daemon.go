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

var daemonFailLog = producer.NewFailureLogger(time.Minute)

func runDaemon() {
	rotateCodexLogs()
	openDaemonLog("ember-codex-producer")
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		fmt.Fprintln(os.Stderr, "codex producer: EMBER_SOURCE/EMBER_SERVER_URL not set; nothing to do")
		os.Exit(0)
	}
	w := newWatcher(cfg)
	client := producer.NewClient(cfg.ServerURL, cfg.Token, httpTimeout)
	if path, err := producer.LinkStatusPath("codex-producer"); err == nil {
		client.WithLinkStatus(producer.NewLinkStatus(path))
	}

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

func openDaemonLog(name string) {
	f, err := producer.OpenDaemonLog(name)
	if err != nil {
		return
	}
	producer.RedirectStandardIO(f)
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

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
