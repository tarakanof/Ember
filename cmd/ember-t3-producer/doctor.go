package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	if home != "" {
		_, _, _ = producer.EnsureSourceInEnv(filepath.Join(home, ".config", "ember", "producer.env"))
	}
	cfg, _ := loadConfig()
	fmt.Println("ember-t3-producer doctor:")
	fmt.Printf("  source      = %q\n", cfg.Source)
	fmt.Printf("  hint: %s\n", producer.SourceHint(cfg.Source))
	fmt.Printf("  server_url  = %q\n", cfg.ServerConfigured)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	for _, l := range producer.ServerReport(ctx, producer.ServerReportInput{Configured: cfg.ServerConfigured, Prefer: cfg.ServerInstance, Home: home}) {
		fmt.Println("  " + l)
	}
	if h := producer.TokenHint(cfg.Token); h != "" {
		fmt.Println("  WARNING: " + h)
	}
	if runtime.GOOS == "linux" {
		for _, l := range producer.UserUnitStatus(producer.ExecRunner, home, systemdUnitName, producer.CurrentUser()) {
			fmt.Println("  " + l)
		}
		return
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Println("  LaunchAgent: NOT installed")
	}
}
