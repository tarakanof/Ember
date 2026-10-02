package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// pluginID is the Ember plugin's install id (<plugin>@<marketplace>), the key
// Claude Code writes under enabledPlugins. See .claude-plugin/marketplace.json.
const pluginID = "ember@ember"

// pluginEnabled reports whether the user settings enable the Ember plugin.
// Project-scoped enables are not seen here; doctor says so.
func pluginEnabled(root map[string]any) bool {
	ep, _ := root["enabledPlugins"].(map[string]any)
	on, _ := ep[pluginID].(bool)
	return on
}

// stripProducerHooks removes every producer entry from a settings.json hooks
// map, dropping events left empty.
func stripProducerHooks(hooksRoot map[string]any) {
	for ev, entries := range hooksRoot {
		list, ok := entries.([]any)
		if !ok {
			continue
		}
		filtered := []any{}
		for _, e := range list {
			if !entryMatchesProducer(e) {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			delete(hooksRoot, ev)
		} else {
			hooksRoot[ev] = filtered
		}
	}
}

// countSettingsProducerHooks counts hook events in settings.json that carry a
// producer entry.
func countSettingsProducerHooks(root map[string]any) int {
	hooksRoot, _ := root["hooks"].(map[string]any)
	n := 0
	for _, entries := range hooksRoot {
		list, _ := entries.([]any)
		for _, e := range list {
			if entryMatchesProducer(e) {
				n++
				break
			}
		}
	}
	return n
}

func readUserSettings(home string) map[string]any {
	root := map[string]any{}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err == nil {
		_ = json.Unmarshal(b, &root)
	}
	return root
}

func pluginEnabledAt(home string) bool {
	return pluginEnabled(readUserSettings(home))
}

// hookRegistrationReport describes where the producer's Claude hooks are
// registered. double is true when both the plugin and settings.json register
// them, which makes every hook POST twice.
func hookRegistrationReport(home string) (line string, double bool) {
	root := readUserSettings(home)
	plugin := pluginEnabled(root)
	n := countSettingsProducerHooks(root)
	switch {
	case plugin && n > 0:
		return fmt.Sprintf("registered TWICE: plugin %s and %d settings.json events, so every hook POSTs twice. "+
			"Fix: run `ember-claude-producer configure` (drops the settings.json copies while the plugin is enabled) "+
			"or `claude plugin disable %s`", pluginID, n, pluginID), true
	case plugin:
		return "plugin " + pluginID + " (enabled in ~/.claude/settings.json)", false
	case n > 0:
		return fmt.Sprintf("~/.claude/settings.json (%d events)", n), false
	default:
		return "NONE in ~/.claude/settings.json (plugin " + pluginID + " not enabled there; " +
			"a project-scoped enable isn't checked)", false
	}
}

func printPluginNote() {
	home, err := os.UserHomeDir()
	if err == nil && pluginEnabledAt(home) {
		fmt.Println("Plugin " + pluginID + " is enabled: it registers the hooks, so settings.json gets only the statusLine.")
	}
}
