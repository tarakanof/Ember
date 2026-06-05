package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const heartbeatInterval = 10 * time.Second

func runTick() {
	rotateProducerLogs()
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dispatchTick(ctx, cfg)
	os.Exit(0)
}

// runDaemon is the long-lived `run` subcommand: a KeepAlive LaunchAgent process
// that re-posts live session markers every heartbeatInterval until SIGINT/SIGTERM.
// It replaces the old per-tick StartInterval one-shot — a StartInterval job has
// no self-recovery, so once launchd evicted it (crash, sleep/wake edge, a
// bootout whose bootstrap didn't follow) it stayed silently unloaded until the
// next login. KeepAlive lets launchd restart this process after any exit.
func runDaemon() {
	rotateProducerLogs()
	// Validate config once up front; with KeepAlive, exiting here just makes
	// launchd retry (throttled) until the operator finishes configuring.
	if cfg, err := loadConfig(); err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Poll the authoritative usage endpoint on its own slower cadence. Always
	// runs when creds + ServerURL are present; whether the widget *displays* is
	// a server-side toggle, so producer config stays unchanged.
	go usagePollLoop(ctx)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		heartbeatPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// heartbeatPass runs one tick. It reloads config every pass so producer.env
// edits (e.g. from the settings window) take effect without a daemon restart —
// preserving the behaviour of the old one-shot, which re-read the file on every
// invocation.
func heartbeatPass(parent context.Context) {
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dispatchTick(ctx, cfg)
}

func dispatchTick(ctx context.Context, cfg Config) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	client := NewClient(cfg)
	staleThreshold := time.Now().Add(-time.Duration(cfg.HeartbeatTTLHours) * time.Hour)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		sessionID := strings.TrimSuffix(e.Name(), ".json")
		markerP := filepath.Join(dir, e.Name())
		lockP := filepath.Join(dir, sessionID+".lock")
		processOneMarker(ctx, cfg, client, markerP, lockP, staleThreshold)
	}
}

func processOneMarker(ctx context.Context, cfg Config, client *Client, markerP, lockP string, staleThreshold time.Time) {
	info, err := os.Stat(markerP)
	if err != nil {
		return
	}
	// Owner-liveness reap: if the Claude process that owns this session has
	// exited (window closed / crash, with no SessionEnd), drop it now instead
	// of keeping it alive via re-POST until the marker TTL. Markers without a
	// recorded owner fall through to the TTL path below.
	if pid, start, ok := markerOwner(markerP); ok && !ownerAlive(pid, start) {
		_ = withLockEx(lockP, func() error {
			pid2, start2, ok2 := markerOwner(markerP)
			if ok2 && ownerAlive(pid2, start2) {
				return nil // owner refreshed (resume) between observation and lock
			}
			body, err := os.ReadFile(markerP)
			if err == nil {
				var req StatusRequest
				if json.Unmarshal(body, &req) == nil {
					_ = client.Delete(ctx, DeleteRequest{
						Source: req.Source, Tool: req.Tool, Session: req.Session,
					})
				}
			}
			_ = os.Remove(markerP)
			return nil
		})
		return
	}
	if info.ModTime().Before(staleThreshold) {
		// Stale: re-stat under exclusive lock, delete if still stale
		_ = withLockEx(lockP, func() error {
			info2, err := os.Stat(markerP)
			if err != nil {
				return nil
			}
			if !info2.ModTime().Before(staleThreshold) {
				return nil // freshly upserted between observation and lock; skip
			}
			body, err := os.ReadFile(markerP)
			if err == nil {
				var req StatusRequest
				if json.Unmarshal(body, &req) == nil {
					_ = client.Delete(ctx, DeleteRequest{
						Source: req.Source, Tool: req.Tool, Session: req.Session,
					})
				}
			}
			_ = os.Remove(markerP)
			return nil
		})
		return
	}
	// Fresh: shared lock + read + POST
	_ = withLockSh(lockP, func() error {
		body, err := os.ReadFile(markerP)
		if err != nil {
			return nil
		}
		var req StatusRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil
		}
		if !cfg.ContextPctEnabled {
			req.ContextPct = nil
		}
		_ = client.Post(ctx, req)
		return nil
	})
}
