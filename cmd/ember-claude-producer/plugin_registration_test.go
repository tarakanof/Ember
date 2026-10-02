package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, home string, v map[string]any) {
	t.Helper()
	mustMkdir(t, filepath.Join(home, ".claude"))
	b, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

const testBin = "/usr/local/bin/ember-claude-producer"

func TestMergeSettings_PluginEnabledDropsSettingsHooksKeepsStatusLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := mergeSettingsJSON(home, testBin); err != nil {
		t.Fatal(err)
	}
	root := readSettings(t, home)
	hooks := root["hooks"].(map[string]any)
	hooks["Stop"] = append(hooks["Stop"].([]any), map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/say done"}},
	})
	root["enabledPlugins"] = map[string]any{pluginID: true}
	writeSettings(t, home, root)

	if err := mergeSettingsJSON(home, testBin); err != nil {
		t.Fatal(err)
	}
	root = readSettings(t, home)
	if n := countSettingsProducerHooks(root); n != 0 {
		t.Errorf("plugin enabled: settings.json still has %d producer hook events (double POSTs)", n)
	}
	stop, _ := root["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 1 {
		t.Errorf("user's own Stop hook must survive, got %v", stop)
	}
	if !statusLineIsOurs(root["statusLine"]) {
		t.Errorf("statusLine must still be installed (plugins can't set it), got %v", root["statusLine"])
	}
}

func TestMergeSettings_PluginDisabledStillRegistersHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSettings(t, home, map[string]any{"enabledPlugins": map[string]any{pluginID: false}})
	if err := mergeSettingsJSON(home, testBin); err != nil {
		t.Fatal(err)
	}
	if n := countSettingsProducerHooks(readSettings(t, home)); n != len(producerHookSpecs) {
		t.Errorf("plugin disabled: %d producer hook events, want %d", n, len(producerHookSpecs))
	}
}

func TestHookRegistrationReport(t *testing.T) {
	cases := []struct {
		name          string
		plugin        bool
		settingsHooks bool
		wantDouble    bool
		wantSub       string
	}{
		{"none", false, false, false, "NONE"},
		{"settings only", false, true, false, "settings.json"},
		{"plugin only", true, false, false, "plugin " + pluginID},
		{"both", true, true, true, "TWICE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if c.settingsHooks {
				if err := mergeSettingsJSON(home, testBin); err != nil {
					t.Fatal(err)
				}
			}
			root := map[string]any{}
			if c.settingsHooks {
				root = readSettings(t, home)
			}
			if c.plugin {
				root["enabledPlugins"] = map[string]any{pluginID: true}
			}
			writeSettings(t, home, root)

			line, double := hookRegistrationReport(home)
			if double != c.wantDouble {
				t.Errorf("double = %v, want %v (line %q)", double, c.wantDouble, line)
			}
			if !strings.Contains(line, c.wantSub) {
				t.Errorf("line %q missing %q", line, c.wantSub)
			}
		})
	}
}
