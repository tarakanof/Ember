package main

import (
	"path/filepath"
	"testing"
)

func TestMenuPrefs_DefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "menu.json")

	// Missing file → defaults.
	got := loadMenuPrefs(path)
	if got != defaultMenuPrefs() {
		t.Errorf("missing file: got %+v want defaults", got)
	}

	// Round-trip a valid set.
	p := menuPrefs{AppIcon: "aurora", TrayClaudeGlyph: "aicode-chat", TrayCodexGlyph: "code-hex", TrayIdleGlyph: "awtrix-screen"}
	if err := saveMenuPrefs(path, p); err != nil {
		t.Fatal(err)
	}
	if got := loadMenuPrefs(path); got != p {
		t.Errorf("round-trip: got %+v want %+v", got, p)
	}
}

func TestMenuPrefs_UnknownValuesFallBack(t *testing.T) {
	d := defaultMenuPrefs()
	bad := menuPrefs{AppIcon: "neon", TrayClaudeGlyph: "nope", TrayCodexGlyph: "code", TrayIdleGlyph: ""}.validate()
	if bad.AppIcon != d.AppIcon || bad.TrayClaudeGlyph != d.TrayClaudeGlyph || bad.TrayIdleGlyph != d.TrayIdleGlyph {
		t.Errorf("unknown values not defaulted: %+v", bad)
	}
	if bad.TrayCodexGlyph != "code" {
		t.Errorf("valid value should survive: %+v", bad)
	}
}
