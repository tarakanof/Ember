package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func runDoctor() {
	home, _ := os.UserHomeDir()
	if home != "" {
		_, _, _ = producer.EnsureSourceInEnv(filepath.Join(home, ".config", "ember", "producer.env"))
	}
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

	envPath := filepath.Join(home, ".config", "ember", "producer.env")
	if info, err := os.Stat(envPath); err == nil {
		fmt.Printf("  producer.env: %s mode=%#o\n", envPath, info.Mode().Perm())
	} else {
		fmt.Printf("  producer.env: MISSING at %s\n", envPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, l := range producer.ServerReport(ctx, producer.ServerReportInput{Configured: cfg.ServerConfigured, Prefer: cfg.ServerInstance, Home: home}) {
		fmt.Println("  " + l)
	}
	if h := producer.TokenHint(cfg.Token); h != "" {
		fmt.Println("  WARNING: " + h)
	}

	hooksLine, _ := hookRegistrationReport(home)
	fmt.Printf("  claude hooks: %s\n", hooksLine)

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

	if runtime.GOOS == "linux" {
		for _, l := range producer.UserUnitStatus(producer.ExecRunner, home, systemdUnitName, currentUser()) {
			fmt.Println("  heartbeat " + l)
		}
		return
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  LaunchAgent: installed at %s\n", plistPath)
	} else {
		fmt.Printf("  LaunchAgent: NOT installed\n")
	}

	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	hint := heartbeatFixHint(producer.ExecLaunchctl, uid, plistPath)
	fmt.Printf("  heartbeat agent: %s\n", heartbeatStatusLine(err == nil, string(out), hint))
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
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
