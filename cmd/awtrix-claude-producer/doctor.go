package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// A plist on disk doesn't mean the agent is loaded. If it isn't, the 10s
	// tick never runs and active sessions are reaped once they pass the server's
	// stale window — the display falls back to the dim idle robot mid-session.
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	hint := fmt.Sprintf("launchctl bootstrap gui/%d %q", uid, plistPath)
	fmt.Printf("  heartbeat agent: %s\n", heartbeatStatusLine(err == nil, string(out), hint))
}

// launchctlField extracts the value of a tab-indented `key = value` line from
// `launchctl print` output. Returns "" when the key is absent.
func launchctlField(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+" = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// heartbeatStatusLine renders the doctor line for the heartbeat LaunchAgent's
// runtime state. loaded is whether `launchctl print` found the service; printOut
// is its output (parsed for runs/last-exit when loaded); hint is the remediation
// command shown when the agent is not loaded.
func heartbeatStatusLine(loaded bool, printOut, hint string) string {
	if !loaded {
		return "NOT loaded — heartbeat ticks aren't running, so active sessions go " +
			"idle after the stale window. Fix: " + hint
	}
	runs := launchctlField(printOut, "runs")
	exit := launchctlField(printOut, "last exit code")
	if runs == "" && exit == "" {
		return "loaded"
	}
	return fmt.Sprintf("loaded (runs=%s, last exit=%s)", dashIfEmpty(runs), dashIfEmpty(exit))
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
