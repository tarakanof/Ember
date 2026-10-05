package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	producer.DoctorPrelude(home)
	cfg, _ := loadConfig()
	fmt.Println("ember-claude-producer doctor:")
	fmt.Printf("  config:\n")
	fmt.Printf("    source     = %q\n", cfg.Source)
	fmt.Printf("    hint: %s\n", producer.SourceHint(cfg.Source))
	fmt.Printf("    server_url = %q\n", cfg.ServerConfigured)
	if cfg.Token == "" {
		fmt.Printf("    token      = (unset)\n")
	} else {
		fmt.Printf("    token      = (set, %d chars)\n", len(cfg.Token))
	}
	fmt.Printf("    heartbeat_ttl_hours = %d\n", cfg.HeartbeatTTLHours)
	fmt.Printf("    hook_timeout_ms     = %d\n", cfg.HookTimeoutMs)
	fmt.Printf("    done_ttl_seconds    = %d\n", cfg.DoneTTLSeconds)

	fmt.Println("  " + producer.EnvFileLine(home))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, l := range producer.ServerLines(ctx, home, cfg.Common) {
		fmt.Println("  " + l)
	}

	hooksLine, _ := hookRegistrationReport(home)
	fmt.Printf("  claude hooks: %s\n", hooksLine)
	if n := legacySettingsBackups(filepath.Join(home, ".claude", "settings.json")); n > 0 {
		fmt.Printf("  hint: %d settings.json.bak.<pid> backups from older installs in ~/.claude; delete them if you don't need them\n", n)
	}
	fmt.Printf("  claude agents cross-check: %s\n", agentsDoctorLine(ctx))

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

	for _, l := range service.Status(home) {
		if runtime.GOOS == "linux" {
			l = "heartbeat " + l
		}
		fmt.Println("  " + l)
	}
	if runtime.GOOS == "linux" {
		return
	}
	plistPath := service.PlistPath(home)
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	hint := heartbeatFixHint(producer.ExecLaunchctl, uid, plistPath)
	fmt.Printf("  heartbeat agent: %s\n", heartbeatStatusLine(err == nil, string(out), hint))
}

const appRepairHint = "open Ember › Settings › Agents and click Repair"

func heartbeatFixHint(lc producer.Launchctl, uid int, plistPath string) string {
	if producer.AppRegistered(lc, fmt.Sprintf("gui/%d", uid), launchAgentLabel) {
		return appRepairHint
	}
	return fmt.Sprintf("launchctl bootstrap gui/%d %q", uid, plistPath)
}

func launchctlField(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+" = "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

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
