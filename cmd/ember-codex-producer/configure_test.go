package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigure_DirsAndEnv_NoLaunchAgent(t *testing.T) {
	home := t.TempDir()
	if err := configureAt(home); err != nil {
		t.Fatalf("configureAt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ember", "producer.env")); err != nil {
		t.Fatalf("producer.env not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "ember", "sessions")); err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")); !os.IsNotExist(err) {
		t.Fatalf("configure must not write a LaunchAgent plist")
	}
}
