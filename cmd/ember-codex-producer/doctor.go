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
	fmt.Println("ember-codex-producer doctor:")
	fmt.Printf("  config:\n")
	fmt.Printf("    source      = %q\n", cfg.Source)
	fmt.Printf("    hint: %s\n", producer.SourceHint(cfg.Source))
	fmt.Printf("    server_url  = %q\n", cfg.ServerConfigured)
	if cfg.Token == "" {
		fmt.Printf("    token       = (unset)\n")
	} else {
		fmt.Printf("    token       = (set, %d chars)\n", len(cfg.Token))
	}
	fmt.Printf("    source_color        = %q\n", cfg.SourceColor)
	fmt.Printf("    context_pct_enabled = %v\n", cfg.ContextPctEnabled)
	fmt.Printf("    rate_pct_enabled    = %v\n", cfg.RatePctEnabled)
	fmt.Printf("    poll_interval_ms    = %d\n", cfg.PollIntervalMs)
	fmt.Printf("    activity_window_s   = %d\n", cfg.ActivityWindowSeconds)
	fmt.Printf("    sessions_dir        = %q\n", cfg.SessionsDir)
	fmt.Printf("    sources             = %s\n", sourceList(cfg.Sources))
	fmt.Printf("    include_claude      = %v\n", cfg.IncludeClaude)

	fmt.Println("  " + producer.EnvFileLine(home))

	if info, err := os.Stat(cfg.SessionsDir); err == nil && info.IsDir() {
		fmt.Printf("  sessions dir: OK (%s)\n", cfg.SessionsDir)
	} else {
		fmt.Printf("  sessions dir: NOT FOUND (%s)\n", cfg.SessionsDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actx, acancel := context.WithTimeout(ctx, 3*time.Second)
	for _, l := range appServerReport(actx, cfg) {
		fmt.Println("  " + l)
	}
	acancel()
	for _, l := range producer.ServerLines(ctx, home, cfg.Common) {
		fmt.Println("  " + l)
	}
	for _, l := range service.Status(home) {
		fmt.Println("  " + l)
	}
}
