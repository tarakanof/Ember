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
	openDaemonLog("ember-tick")
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

// openDaemonLog routes stdout/stderr (both the OS fds and the Go-level
// variables) and the slog default logger to ~/Library/Logs/<name>.log so the
// daemon logs correctly even when launched from a plist with no
// StandardOutPath (a bundled static plist can't encode a per-user path).
// Best-effort: on failure it silently leaves stderr as-is.
func openDaemonLog(name string) {
	f, err := producer.OpenDaemonLog(name)
	if err != nil {
		return
	}
	producer.RedirectStandardIO(f)
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

// statuslineUsageSnapshot is the statusline-derived rate-limit data found on
// one live Claude marker during a heartbeat pass. updatedAt is the marker
// file's mtime, used by dispatchTick to pick the single freshest session's
// data when more than one Claude session is live (mirrors the server's own
// effectiveFiveHour "newest session wins" heuristic).
type statuslineUsageSnapshot struct {
	fiveHourPct        *int
	fiveHourResetAt    int64
	fiveHourResetLabel string
	sevenDayPct        *int
	sevenDayResetAt    int64
	sevenDayResetLabel string
	updatedAt          time.Time
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
	var best *statuslineUsageSnapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		sessionID := strings.TrimSuffix(e.Name(), ".json")
		markerP := filepath.Join(dir, e.Name())
		lockP := filepath.Join(dir, sessionID+".lock")
		if snap := processOneMarker(ctx, cfg, client, markerP, lockP, staleThreshold); snap != nil {
			if best == nil || snap.updatedAt.After(best.updatedAt) {
				best = snap
			}
		}
	}
	// Relay the freshest live session's rate-limit data to /v1/usage, making
	// the statusline the primary weekly (and 5h) source: this runs every
	// heartbeat (10s) while a session is active, far more often than the
	// OAuth poller's usagePollInterval (5m) — and unlike that poller, it
	// never depends on the flaky reverse-engineered OAuth usage endpoint.
	if best != nil {
		postStatuslineUsage(ctx, client, *best)
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

// processOneMarker re-POSTs or reaps one live Claude marker. It returns the
// marker's statusline-derived rate-limit data (nil when the marker carried
// none, or wasn't reached via the "fresh" path) for dispatchTick to relay to
// /v1/usage.
func processOneMarker(ctx context.Context, cfg Config, client *Client, markerP, lockP string, staleThreshold time.Time) *statuslineUsageSnapshot {
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
		return nil
	}
	info, err := os.Stat(markerP)
	if err != nil {
		return nil
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
		return nil
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
		return nil
	}
	// Fresh: shared lock + read + POST
	var snap *statuslineUsageSnapshot
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
		// Capture the statusline-owned rate-limit fields for the /v1/usage
		// relay before stripping the weekly ones below — they're marker-only
		// and must never appear on the /v1/status wire payload.
		if req.RateWindowPct != nil || req.RateWeekPct != nil {
			snap = &statuslineUsageSnapshot{
				fiveHourPct:        req.RateWindowPct,
				fiveHourResetAt:    req.RateResetAt,
				fiveHourResetLabel: req.RateResetLabel,
				sevenDayPct:        req.RateWeekPct,
				sevenDayResetAt:    req.RateWeekResetAt,
				sevenDayResetLabel: req.RateWeekResetLabel,
				updatedAt:          info.ModTime(),
			}
		}
		req.RateWeekPct = nil
		req.RateWeekResetAt = 0
		req.RateWeekResetLabel = ""
		if err := client.Post(ctx, req); err != nil {
			tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
		}
		return nil
	})
	return snap
}
