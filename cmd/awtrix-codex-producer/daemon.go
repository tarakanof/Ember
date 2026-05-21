package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dt/awtrix-ai-status/internal/producer"
)

const httpTimeout = 5 * time.Second

// runDaemon is the entry point for `awtrix-codex-producer run` (and the bare
// default). It polls until SIGINT/SIGTERM.
func runDaemon() {
	rotateCodexLogs()
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		fmt.Fprintln(os.Stderr, "codex producer: STATUS_SOURCE/STATUS_SERVER_URL not set; nothing to do")
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
			return
		case <-ticker.C:
		}
	}
}

// runOnce performs a single reconcile pass, issuing the resulting POSTs/DELETEs.
func runOnce(ctx context.Context, w *watcher, client *producer.Client) {
	posts, deletes := w.tick()
	for _, req := range posts {
		_ = client.Post(ctx, req)
	}
	for _, req := range deletes {
		_ = client.Delete(ctx, req)
	}
}

func rotateCodexLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	producer.RotateLogIfLarge(filepath.Join(home, "Library", "Logs", "awtrix-codex-producer.log"), producer.DefaultLogThreshold)
}
