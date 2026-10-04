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
	if err := deconfigureAt(home); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: deconfigure:", err)
	}
	if runtime.GOOS == "linux" {
		if err := producer.UninstallUserUnit(producer.ExecRunner, home, systemdUnitName); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall: systemd unit:", err)
		}
		fmt.Println("Uninstall complete. producer.env was left in place (shared with the Claude producer).")
		return
	}
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	switch producer.AgentOwner(producer.ExecLaunchctl, target, plistPath) {
	case producer.OwnedByCLI:
		_, _ = producer.ExecLaunchctl("bootout", target)
	case producer.OwnedByOther:
		fmt.Fprintf(os.Stderr, "uninstall: left %s loaded: it's Ember.app's (turn reporting off in Ember › Settings › Agents)\n", target)
	case producer.NotLoaded:
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "uninstall: remove plist:", err)
	}
	fmt.Println("Uninstall complete. producer.env was left in place (shared with the Claude producer).")
}

func runDeconfigure() {
	if err := deconfigure(); err != nil {
		fmt.Fprintln(os.Stderr, "deconfigure failed:", err)
		os.Exit(1)
	}
	fmt.Println("Deconfigure complete.")
}

func deconfigureAt(home string) error {
	return nil
}

func deconfigure() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return deconfigureAt(home)
}
