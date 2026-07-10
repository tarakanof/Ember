package main

import (
	"os"
	"path/filepath"
	"testing"
)

// configure must create dirs, seed producer.env, and merge settings.json —
// but must NOT write a LaunchAgent plist (that is daemon-activation, owned by
// install()/the app).
func TestConfigure_DoesFileWork_NoLaunchAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := configureAt(home, "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"); err != nil {
		t.Fatalf("configureAt: %v", err)
	}
	// settings.json got our hooks.
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || len(b) == 0 {
		t.Fatalf("settings.json not written: %v", err)
	}
	// producer.env seeded.
	if _, err := os.Stat(filepath.Join(home, ".config", "ember", "producer.env")); err != nil {
		t.Fatalf("producer.env not seeded: %v", err)
	}
	// NO LaunchAgent plist.
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")); !os.IsNotExist(err) {
		t.Fatalf("configure must not write a LaunchAgent plist; stat err = %v", err)
	}
}
