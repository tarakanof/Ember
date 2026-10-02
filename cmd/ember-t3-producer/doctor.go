package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	cfg, _ := loadConfig()
	fmt.Println("ember-t3-producer doctor:")
	fmt.Printf("  source      = %q\n", cfg.Source)
	fmt.Printf("  server_url  = %q\n", cfg.ServerURL)
	if cfg.Token == "" {
		fmt.Println("  token       = (unset)")
	} else {
		fmt.Printf("  token       = (set, %d chars)\n", len(cfg.Token))
	}
	fmt.Printf("  t3_home     = %q\n", cfg.T3Home)
	fmt.Printf("  poll_ms     = %d, activity_window_s = %d\n", cfg.PollIntervalMs, cfg.ActivityWindowSeconds)

	if serverAlive(cfg.T3Home, pidAlive) {
		fmt.Println("  T3 server: running")
	} else {
		fmt.Println("  T3 server: NOT running (no live userdata/server-runtime.json)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if snap, err := readSnapshot(ctx, cfg.T3Home); err != nil {
		fmt.Printf("  T3 state: %v\n", err)
	} else {
		note := ""
		if snap.Migration > pinnedMigrations[snap.Schema] {
			note = fmt.Sprintf(" (newer than verified %d)", pinnedMigrations[snap.Schema])
		}
		fmt.Printf("  T3 state: schema v%d, migration %d%s, %d threads\n", snap.Schema, snap.Migration, note, len(snap.Threads))
	}

	switch {
	case cfg.ServerURL == "":
		fmt.Println("  ember server: (no server_url configured)")
	case serverReachable(cfg.ServerURL, 2*time.Second):
		fmt.Printf("  ember server: reachable (%s)\n", cfg.ServerURL)
	default:
		fmt.Printf("  ember server: UNREACHABLE (%s)\n", cfg.ServerURL)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Println("  LaunchAgent: NOT installed")
	}
}

func serverReachable(url string, timeout time.Duration) bool {
	if url == "" {
		return false
	}
	resp, err := (&http.Client{Timeout: timeout}).Get(url + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
