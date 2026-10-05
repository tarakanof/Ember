package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	producer.DoctorPrelude(home)
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

	for _, l := range producer.ServerLines(ctx, home, cfg.Common) {
		fmt.Println("  " + l)
	}
	for _, l := range service.Status(home) {
		fmt.Println("  " + l)
	}
}
