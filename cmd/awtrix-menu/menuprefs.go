package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// menuPrefs are menu-app-local icon choices, stored separately from producer.env.
type menuPrefs struct {
	AppIcon         string `json:"app_icon"`
	TrayClaudeGlyph string `json:"tray_claude_glyph"`
	TrayCodexGlyph  string `json:"tray_codex_glyph"`
	TrayIdleGlyph   string `json:"tray_idle_glyph"`
}

func defaultMenuPrefs() menuPrefs {
	return menuPrefs{
		AppIcon:         "multicolor-rgb",
		TrayClaudeGlyph: "aicode",
		TrayCodexGlyph:  "code",
		TrayIdleGlyph:   "awtrix",
	}
}

func menuPrefsPath(home string) string {
	return filepath.Join(home, ".config", "awtrix-ai-status", "menu.json")
}

func inSet(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// validate replaces any unknown value with its default.
func (p menuPrefs) validate() menuPrefs {
	d := defaultMenuPrefs()
	if !inSet(p.AppIcon, appIconPalettes) {
		p.AppIcon = d.AppIcon
	}
	if !inSet(p.TrayClaudeGlyph, trayGlyphs) {
		p.TrayClaudeGlyph = d.TrayClaudeGlyph
	}
	if !inSet(p.TrayCodexGlyph, trayGlyphs) {
		p.TrayCodexGlyph = d.TrayCodexGlyph
	}
	if !inSet(p.TrayIdleGlyph, trayGlyphs) {
		p.TrayIdleGlyph = d.TrayIdleGlyph
	}
	return p
}

// loadMenuPrefs reads + validates prefs; missing/corrupt → defaults.
func loadMenuPrefs(path string) menuPrefs {
	b, err := os.ReadFile(path)
	if err != nil {
		return defaultMenuPrefs()
	}
	var p menuPrefs
	if json.Unmarshal(b, &p) != nil {
		return defaultMenuPrefs()
	}
	return p.validate()
}

// saveMenuPrefs validates then atomically writes prefs (0600).
func saveMenuPrefs(path string, p menuPrefs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p.validate(), "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "menu.tmp-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
