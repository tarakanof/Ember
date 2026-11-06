package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHookRegistrationFixturesMatchExpected(t *testing.T) {
	dir := filepath.Join("testdata", "hook-registration")
	raw, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]struct {
		Plugin         bool `json:"plugin"`
		SettingsEvents int  `json:"settings_events"`
		Unreadable     bool `json:"unreadable"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("expected.json lists no fixtures")
	}
	for name, w := range want {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			mustMkdir(t, filepath.Join(home, ".claude"))
			if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			root := readUserSettings(home)
			if got := pluginEnabled(root); got != w.Plugin {
				t.Errorf("pluginEnabled = %v, want %v", got, w.Plugin)
			}
			if got := countSettingsProducerHooks(root); got != w.SettingsEvents {
				t.Errorf("countSettingsProducerHooks = %d, want %d", got, w.SettingsEvents)
			}
			var parsed map[string]any
			if got := json.Unmarshal(body, &parsed) != nil; got != w.Unreadable {
				t.Errorf("unreadable = %v, want %v", got, w.Unreadable)
			}
		})
	}
}
