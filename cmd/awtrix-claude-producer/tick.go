package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
		if cfg.ContextPctEnabled {
			if pct, err := computeContextPct(req.Session, cfg.ContextWindowTokens); err == nil && pct != nil {
				req.ContextPct = pct
			}
		}
		_ = client.Post(ctx, req)
		return nil
	})
}
