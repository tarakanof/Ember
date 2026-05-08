package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	cfg, _ := loadConfig()
	fmt.Println("awtrix-claude-producer doctor:")
	fmt.Printf("  config:\n")
	fmt.Printf("    source     = %q\n", cfg.Source)
	fmt.Printf("    server_url = %q\n", cfg.ServerURL)
	if cfg.Token == "" {
		fmt.Printf("    token      = (unset)\n")
	} else {
		fmt.Printf("    token      = (set, %d chars)\n", len(cfg.Token))
	}
	fmt.Printf("    heartbeat_ttl_hours = %d\n", cfg.HeartbeatTTLHours)
	fmt.Printf("    hook_timeout_ms     = %d\n", cfg.HookTimeoutMs)

	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
	if info, err := os.Stat(envPath); err == nil {
		fmt.Printf("  producer.env: %s mode=%#o\n", envPath, info.Mode().Perm())
	} else {
		fmt.Printf("  producer.env: MISSING at %s\n", envPath)
	}

	stateD, _ := stateDir()
	if entries, err := os.ReadDir(stateD); err == nil {
		count := 0
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				count++
			}
		}
		fmt.Printf("  active markers: %d in %s\n", count, stateD)
	} else {
		fmt.Printf("  state dir: not present (%s)\n", stateD)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Printf("  LaunchAgent: NOT installed\n")
	}
}
