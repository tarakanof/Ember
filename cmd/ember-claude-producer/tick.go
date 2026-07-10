package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const heartbeatInterval = 10 * time.Second

// tickFailLog throttles POST/DELETE failure warnings across daemon ticks
// (~1/min per failure kind). It's a process-lifetime singleton rather than a
// per-call value because dispatchTick builds a fresh *Client every pass
// (heartbeatPass reloads config live, see below) — state that should persist
// across passes has to live outside that per-pass client.
var tickFailLog = producer.NewFailureLogger(time.Minute)

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
	client := NewDaemonClient(cfg)
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

// markerTool decodes just the "tool" field from a marker file, without
// requiring the full StatusRequest shape. Used to gate the Claude daemon's
// marker scan to Claude-owned markers only. ok is false when the file is
// missing or not valid JSON.
func markerTool(markerP string) (tool string, ok bool) {
	body, err := os.ReadFile(markerP)
	if err != nil {
		return "", false
	}
	var m struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return "", false
	}
	return m.Tool, true
}

func processOneMarker(ctx context.Context, cfg Config, client *Client, markerP, lockP string, staleThreshold time.Time) {
	// Ownership gate: ~/.local/state/ember/sessions is shared with the Codex
	// producer (cmd/ember-codex-producer), which writes its own markers with
	// "tool":"codex" and already fully owns their lifecycle (POST/reap/DELETE
	// via its own daemon). Before this gate, the Claude heartbeat re-POSTed
	// (resurrecting sessions the Codex daemon had just DELETEd), reaped
	// crash-orphaned Codex markers on its own TTL, and stamped Claude-only
	// settings (SourceCard/SessionBar, ContextPctEnabled) onto Codex sessions.
	//
	// Investigation (both marker write paths, verified in this tree): the
	// Claude producer always writes an explicit "tool":"claude" (hook.go
	// handleUpsert/handleDelete), and the Codex producer always writes an
	// explicit "tool":"codex" (ember-codex-producer/watcher.go, tail.go).
	// Neither producer ever emits an empty/missing Tool today, so an
	// empty/missing Tool can only be a marker written before this field
	// existed (pre-upgrade) — by definition written by the (older) Claude
	// producer, since Codex marker-writing was introduced after the field.
	// Decision: treat missing/empty Tool as "claude" for that backward
	// compatibility case; skip any other non-empty, non-"claude" Tool
	// entirely (no Stat, no lock, no read past this check, no POST/DELETE).
	if tool, ok := markerTool(markerP); ok && tool != "" && tool != "claude" {
		return
	}
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
					if err := client.Delete(ctx, DeleteRequest{
						Source: req.Source, Tool: req.Tool, Session: req.Session,
					}); err != nil {
						tickFailLog.Warn(slog.Default(), "claude_delete", "status DELETE failed", "err", err)
					}
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
					if err := client.Delete(ctx, DeleteRequest{
						Source: req.Source, Tool: req.Tool, Session: req.Session,
					}); err != nil {
						tickFailLog.Warn(slog.Default(), "claude_delete", "status DELETE failed", "err", err)
					}
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
		sc, sb := cfg.SourceCardEnabled, cfg.SessionBarEnabled
		req.SourceCard, req.SessionBar = &sc, &sb
		if err := client.Post(ctx, req); err != nil {
			tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
		}
		return nil
	})
}
