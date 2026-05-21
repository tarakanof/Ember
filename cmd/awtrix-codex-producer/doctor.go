package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	cfg, _ := loadConfig()
	fmt.Println("awtrix-codex-producer doctor:")
	fmt.Printf("  config:\n")
	fmt.Printf("    source      = %q\n", cfg.Source)
	fmt.Printf("    server_url  = %q\n", cfg.ServerURL)
	if cfg.Token == "" {
		fmt.Printf("    token       = (unset)\n")
	} else {
		fmt.Printf("    token       = (set, %d chars)\n", len(cfg.Token))
	}
	fmt.Printf("    source_color        = %q\n", cfg.SourceColor)
	fmt.Printf("    context_pct_enabled = %v\n", cfg.ContextPctEnabled)
	fmt.Printf("    poll_interval_ms    = %d\n", cfg.PollIntervalMs)
	fmt.Printf("    activity_window_s   = %d\n", cfg.ActivityWindowSeconds)
	fmt.Printf("    sessions_dir        = %q\n", cfg.SessionsDir)

	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
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

	if cfg.ServerURL == "" {
		fmt.Printf("  server: (no server_url configured)\n")
	} else if serverReachable(cfg.ServerURL, 2*time.Second) {
		fmt.Printf("  server: reachable (%s)\n", cfg.ServerURL)
	} else {
		fmt.Printf("  server: UNREACHABLE (%s)\n", cfg.ServerURL)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Printf("  LaunchAgent: NOT installed\n")
	}
}

// serverReachable reports whether GET <url>/healthz returns 2xx within timeout.
func serverReachable(url string, timeout time.Duration) bool {
	if url == "" {
		return false
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
