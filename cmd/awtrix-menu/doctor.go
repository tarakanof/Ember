package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
	stateDir := filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", menuLabel+".plist")

	fmt.Println("awtrix-menu doctor:")
	rec, _ := readEnv(envPath)
	src, srvURL, tokSet := "", "", false
	if rec != nil {
		src, srvURL = rec.get("STATUS_SOURCE"), rec.get("STATUS_SERVER_URL")
		tokSet = rec.get("STATUS_TOKEN") != ""
	}
	fmt.Printf("  STATUS_SOURCE     = %q\n", src)
	fmt.Printf("  STATUS_SERVER_URL = %q\n", srvURL)
	fmt.Printf("  STATUS_TOKEN      = %s\n", map[bool]string{true: "(set)", false: "(unset)"}[tokSet])

	view := readView(stateDir, 6*time.Hour)
	fmt.Printf("  Active sessions: %d (dominant: %q)\n", view.ActiveCount, view.DominantState)

	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Printf("  LaunchAgent: NOT installed\n")
	}
}
