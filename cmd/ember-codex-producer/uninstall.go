package main

import (
	"fmt"
	"os"
	"path/filepath"

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
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	// A job Ember.app registered under the same label is left running (#142).
	if err := producer.CheckNotAppManaged(producer.ExecLaunchctl, target); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: not unloading:", err)
	} else {
		producer.BootoutLegacyAgent(producer.ExecLaunchctl, target)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
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

// deconfigureAt reverses configureAt's settings-independent changes for the
// given home. Codex has no settings.json — its only owned config is
// producer.env, which uninstall deliberately leaves in place (it's shared
// with the Claude producer). There is nothing left for deconfigure to remove,
// so this is a defined no-op kept for symmetry with the Claude producer and
// as an extension point if codex ever gains its own config file.
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
