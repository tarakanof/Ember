package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const pluginID = "ember@ember"

func pluginEnabled(root map[string]any) bool {
	ep, _ := root["enabledPlugins"].(map[string]any)
	on, _ := ep[pluginID].(bool)
	return on
}

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

func hookRegistrationReport(home string) (line string, double bool) {
	line, double = hookRegistrationSources(home)
	if !hooksEnabledAt(home) {
		line = "DISABLED by deconfigure/uninstall (" + hooksDisabledPath(home) +
			"); run `ember-claude-producer configure` to re-enable. Registered: " + line
	}
	return line, double
}

func hookRegistrationSources(home string) (line string, double bool) {
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
			"a project-scoped enable isn't checked). If you uninstalled or disabled the plugin, " +
			"run `ember-claude-producer configure` to re-register settings.json hooks", false
	}
}

func printPluginNote() {
	home, err := os.UserHomeDir()
	if err == nil && pluginEnabledAt(home) {
		fmt.Println("Plugin " + pluginID + " is enabled: it registers the hooks, so settings.json gets only the statusLine.")
	}
}

func hooksDisabledPath(home string) string {
	return filepath.Join(home, ".config", "ember", "claude-hooks.disabled")
}

func hooksEnabledAt(home string) bool {
	_, err := os.Stat(hooksDisabledPath(home))
	return os.IsNotExist(err)
}

const hooksDisabledNote = "Written by `ember-claude-producer deconfigure`/`uninstall`: Claude hooks exit without reporting.\n" +
	"`ember-claude-producer configure` (or install, or Ember.app's Agents toggle) removes it.\n"

func disableHooks(home string) error {
	p := hooksDisabledPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(hooksDisabledNote), 0o600)
}

func enableHooks(home string) error {
	if err := os.Remove(hooksDisabledPath(home)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func pluginStillEnabledWarning(home string) string {
	if !pluginEnabledAt(home) {
		return ""
	}
	return "plugin " + pluginID + " is still enabled: its hooks stay registered but exit silently until the next configure. " +
		"To remove them, run: claude plugin disable " + pluginID
}

func warnPluginStillEnabled() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if w := pluginStillEnabledWarning(home); w != "" {
		fmt.Fprintln(os.Stderr, "note:", w)
	}
}
