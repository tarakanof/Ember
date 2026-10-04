package main

import (
	"bytes"
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

func runDaemon() {
	rotateProducerLogs()
	openDaemonLog("ember-tick")
	if path, err := producer.LinkStatusPath("claude-producer"); err == nil {
		daemonLink = producer.NewLinkStatus(path)
	}
	if cfg, err := loadConfig(); err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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

func heartbeatPass(parent context.Context) {
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dispatchTick(ctx, cfg)
}

func openDaemonLog(name string) {
	f, err := producer.OpenDaemonLog(name)
	if err != nil {
		return
	}
	producer.RedirectStandardIO(f)
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

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
	if best != nil {
		postStatuslineUsage(ctx, client, *best)
	}
}

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

func processOneMarker(ctx context.Context, cfg Config, client *Client, markerP, lockP string, staleThreshold time.Time) *statuslineUsageSnapshot {
	if tool, ok := markerTool(markerP); ok && tool != "" && tool != "claude" {
		return nil
	}
	info, err := os.Stat(markerP)
	if err != nil {
		return nil
	}
	// Network calls never run under the session lock: hooks take it on the
	// claude CLI's hot path, and a POST to an unreachable server would stall
	// every one of them for the full daemon timeout (#258).
	if pid, start, ok := markerOwner(markerP); ok && !ownerAlive(pid, start) {
		var gone *StatusRequest
		_ = withLockEx(lockP, func() error {
			pid2, start2, ok2 := markerOwner(markerP)
			if ok2 && ownerAlive(pid2, start2) {
				return nil
			}
			gone = removeMarker(markerP)
			return nil
		})
		deleteSession(ctx, client, gone)
		return nil
	}
	if info.ModTime().Before(staleThreshold) {
		var gone *StatusRequest
		_ = withLockEx(lockP, func() error {
			info2, err := os.Stat(markerP)
			if err != nil {
				return nil
			}
			if !info2.ModTime().Before(staleThreshold) {
				return nil
			}
			gone = removeMarker(markerP)
			return nil
		})
		deleteSession(ctx, client, gone)
		return nil
	}
	body, ok := snapshotMarker(markerP, lockP)
	if !ok {
		return nil
	}
	var req StatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	var snap *statuslineUsageSnapshot
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
	if err := client.Post(ctx, wireRequest(cfg, req)); err != nil {
		tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
		return snap
	}
	// A hook may have changed or removed the marker (and told the server)
	// while this POST was in flight, so the server could now hold the older
	// snapshot. Re-send whatever the marker says now; a vanished marker means
	// SessionEnd deleted the session, and this POST must not resurrect it.
	now, ok := snapshotMarker(markerP, lockP)
	switch {
	case !ok:
		deleteSession(ctx, client, &req)
	case !bytes.Equal(now, body):
		var cur StatusRequest
		if json.Unmarshal(now, &cur) == nil {
			if err := client.Post(ctx, wireRequest(cfg, cur)); err != nil {
				tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
			}
		}
	}
	return snap
}

// snapshotMarker reads the marker under a shared lock; ok is false when it is gone.
func snapshotMarker(markerP, lockP string) (body []byte, ok bool) {
	_ = withLockSh(lockP, func() error {
		b, err := os.ReadFile(markerP)
		if err == nil {
			body, ok = b, true
		}
		return nil
	})
	return body, ok
}

// removeMarker deletes the marker (caller holds the lock) and returns the
// session it described, for a DELETE after the lock is released.
func removeMarker(markerP string) *StatusRequest {
	body, err := os.ReadFile(markerP)
	_ = os.Remove(markerP)
	if err != nil {
		return nil
	}
	var req StatusRequest
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	return &req
}

func deleteSession(ctx context.Context, client *Client, req *StatusRequest) {
	if req == nil {
		return
	}
	if err := client.Delete(ctx, DeleteRequest{
		Source: req.Source, Tool: req.Tool, Session: req.Session,
	}); err != nil {
		tickFailLog.Warn(slog.Default(), "claude_delete", "status DELETE failed", "err", err)
	}
}
