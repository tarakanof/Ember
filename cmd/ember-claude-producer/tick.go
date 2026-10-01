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
	if pid, start, ok := markerOwner(markerP); ok && !ownerAlive(pid, start) {
		_ = withLockEx(lockP, func() error {
			pid2, start2, ok2 := markerOwner(markerP)
			if ok2 && ownerAlive(pid2, start2) {
				return nil
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
		_ = withLockEx(lockP, func() error {
			info2, err := os.Stat(markerP)
			if err != nil {
				return nil
			}
			if !info2.ModTime().Before(staleThreshold) {
				return nil
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
		}
		return nil
	})
	return snap
}
