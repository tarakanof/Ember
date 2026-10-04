package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tarakanof/ember/internal/producer"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	if runtime.GOOS == "linux" {
		if err := producer.UninstallUserUnit(producer.ExecRunner, home, systemdUnitName); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall: systemd unit:", err)
		}
		if cfg, err := loadConfig(); err == nil {
			sweepMarkers(cfg.StateDir)
		}
		fmt.Println("Uninstall complete. producer.env was left in place (shared with the other producers).")
		return
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	switch producer.AgentOwner(producer.ExecLaunchctl, target, plistPath) {
	case producer.OwnedByCLI:
		_, _ = producer.ExecLaunchctl("bootout", target)
	case producer.OwnedByOther:
		fmt.Fprintf(os.Stderr, "uninstall: left %s loaded: it's Ember.app's\n", target)
	case producer.NotLoaded:
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "uninstall: remove plist:", err)
	}
	if cfg, err := loadConfig(); err == nil {
		sweepMarkers(cfg.StateDir)
	}
	fmt.Println("Uninstall complete. producer.env was left in place (shared with the other producers).")
}
