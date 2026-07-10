package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigureDeconfigure_RoundTripRestoresSettings characterizes the
// existing configure/deconfigure restore behavior after the configure/
// deconfigure split (Task 1/3): a user's pre-existing statusLine survives a
// configure -> deconfigure round-trip verbatim, and the producer's hooks are
// fully removed by deconfigure.
func TestConfigureDeconfigure_RoundTripRestoresSettings(t *testing.T) {
	home := t.TempDir()
	orig := []byte(`{"statusLine":{"type":"command","command":"my-own-status"}}` + "\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), orig, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bin := "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"
	if err := configureAt(home, bin); err != nil {
		t.Fatalf("configureAt: %v", err)
	}
	if err := deconfigureAt(home); err != nil {
		t.Fatalf("deconfigureAt: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(got), "my-own-status") {
		t.Fatalf("user statusLine not restored after round-trip: %s", got)
	}
	if strings.Contains(string(got), "ember-claude-producer") {
		t.Fatalf("producer hooks not removed after deconfigure: %s", got)
	}
}
