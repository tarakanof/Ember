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

	envPath := filepath.Join(home, ".config", "ember", "producer.env")
	if info, err := os.Stat(envPath); err == nil {
		fmt.Printf("  producer.env: %s mode=%#o\n", envPath, info.Mode().Perm())
	} else {
		fmt.Printf("  producer.env: MISSING at %s\n", envPath)
	}

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
	for _, l := range producer.ServerReport(ctx, producer.ServerReportInput{Configured: cfg.ServerConfigured, Prefer: cfg.ServerInstance, Home: home}) {
		fmt.Println("  " + l)
	}
	if h := producer.TokenHint(cfg.Token); h != "" {
		fmt.Println("  WARNING: " + h)
	}
	for _, l := range serviceStatus(home) {
		fmt.Println("  " + l)
	}
}

func serviceStatus(home string) []string {
	if runtime.GOOS == "linux" {
		return producer.UserUnitStatus(producer.ExecRunner, home, systemdUnitName, producer.CurrentUser())
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		return []string{"LaunchAgent: installed at " + plistPath}
	}
	return []string{"LaunchAgent: NOT installed"}
}
