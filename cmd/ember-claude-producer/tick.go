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
	producer.RotateLogs(producerLogs...)
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
	producer.StartDaemonLog("ember-tick", "ember-claude-producer")
	if path, err := producer.LinkStatusPath("claude-producer"); err == nil {
		daemonLink = producer.NewLinkStatus(path)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || (cfg.ServerURL == "" && !cfg.ServerAuto) {
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	auto, ok := producer.DaemonServer(ctx, home, cfg.ServerURL, cfg.ServerAuto, cfg.ServerInstance)
	if !ok {
		return
	}
	daemonServer = auto
	go usagePollLoop(ctx)
	go agentsWatchLoop(ctx)
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

// loadDaemonConfig is loadConfig with the daemon's discovered server URL
// (fresher than the cache file when a re-browse just moved it).
func loadDaemonConfig() (Config, error) {
	cfg, err := loadConfig()
	if err == nil && cfg.ServerAuto && daemonServer != nil {
		if u := daemonServer.URL(); u != "" {
			cfg.ServerURL = u
		}
	}
	return cfg, err
}

func heartbeatPass(parent context.Context) {
	cfg, err := loadDaemonConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dispatchTick(ctx, cfg)
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
		reapMarker(ctx, client, markerP, lockP)
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
	body, ok := snapshotMarker(markerP, lockP, -1)
	if !ok {
		return nil
	}
	var m marker
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	req := m.StatusRequest
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
	if !heartbeatDue(cfg, m, info.ModTime(), time.Now()) {
		return snap
	}
	if err := postReconciled(ctx, cfg, client, markerP, lockP, body, -1); err != nil {
		tickFailLog.Warn(slog.Default(), "claude_post", "status POST failed", "err", err)
	}
	return snap
}

// heartbeatDue reports whether the heartbeat should re-post the marker.
// done and error are re-posted only until DoneTTLSeconds after the state
// changed (covering a lost hook POST), so the server's done_ttl linger can
// expire and the idle screen return; running and waiting always are.
func heartbeatDue(cfg Config, m marker, mtime, now time.Time) bool {
	if m.State != "done" && m.State != "error" {
		return true
	}
	changed := mtime
	if m.StateChangedAt != 0 {
		changed = time.Unix(m.StateChangedAt, 0)
	}
	return now.Sub(changed) < time.Duration(cfg.DoneTTLSeconds)*time.Second
}

// postReconciled POSTs the marker body, then re-reads the marker (lockWait
// bounds the shared lock; negative blocks). The POST ran outside the lock, so
// a hook may have changed or removed the marker, and told the server, while
// it was in flight: a changed marker is re-sent once (a later change racing
// that re-send heals at the next heartbeat), and a vanished one means
// SessionEnd deleted the session, so it is DELETEd again rather than left as
// a ghost. The check runs even when the POST failed: a timed-out POST may
// still have been applied.
func postReconciled(ctx context.Context, cfg Config, client *Client, markerP, lockP string, body []byte, lockWait time.Duration) error {
	var req StatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	postErr := client.Post(ctx, wireRequest(cfg, req))
	now, ok := snapshotMarker(markerP, lockP, lockWait)
	switch {
	case !ok:
		if _, err := os.Stat(markerP); os.IsNotExist(err) {
			deleteSession(ctx, client, &req)
		}
	case !bytes.Equal(now, body):
		var cur marker
		if json.Unmarshal(now, &cur) == nil && heartbeatDue(cfg, cur, time.Now(), time.Now()) {
			if err := client.Post(ctx, wireRequest(cfg, cur.StatusRequest)); err != nil && postErr == nil {
				postErr = err
			}
		}
	}
	return postErr
}

// snapshotMarker reads the marker under a shared lock; ok is false when it is
// gone or the lock stayed busy for wait (negative blocks).
func snapshotMarker(markerP, lockP string, wait time.Duration) (body []byte, ok bool) {
	_ = withLockShWait(lockP, wait, func() error {
		b, err := os.ReadFile(markerP)
		if err == nil {
			body, ok = b, true
		}
		return nil
	})
	return body, ok
}

// reapMarker removes a marker whose owner process is gone, re-checking the
// owner under the lock, and DELETEs the session after releasing it.
func reapMarker(ctx context.Context, client *Client, markerP, lockP string) {
	var gone *StatusRequest
	_ = withLockEx(lockP, func() error {
		pid, start, ok := markerOwner(markerP)
		if ok && ownerAlive(pid, start) {
			return nil
		}
		gone = removeMarker(markerP)
		return nil
	})
	deleteSession(ctx, client, gone)
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
