package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const httpTimeout = 5 * time.Second

// runDaemon is the entry point for `ember-codex-producer run` (and the bare
// default). It polls until SIGINT/SIGTERM.
func runDaemon() {
	rotateCodexLogs()
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

// runOnce performs a single reconcile pass, issuing POSTs/DELETEs and keeping
// the menu-bar marker files in sync (write on POST, remove on DELETE).
func runOnce(ctx context.Context, w *watcher, client *producer.Client) {
	posts, deletes := w.tick()
	for _, req := range posts {
		_ = client.Post(ctx, req)
		if body, err := json.Marshal(req); err == nil {
			_ = writeMarker(w.cfg.StateDir, req.Session, body)
		}
	}
	for _, req := range deletes {
		_ = client.Delete(ctx, req)
		_ = removeMarker(w.cfg.StateDir, req.Session)
	}
}

func rotateCodexLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	producer.RotateLogIfLarge(filepath.Join(home, "Library", "Logs", "ember-codex-producer.log"), producer.DefaultLogThreshold)
}
