package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigure_DoesFileWork_NoLaunchAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := configureAt(home, "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"); err != nil {
		t.Fatalf("configureAt: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || len(b) == 0 {
		t.Fatalf("settings.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ember", "producer.env")); err != nil {
		t.Fatalf("producer.env not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")); !os.IsNotExist(err) {
		t.Fatalf("configure must not write a LaunchAgent plist; stat err = %v", err)
	}
}
