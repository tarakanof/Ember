package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover upgrading from the pre-rebrand binary
// (awtrix-claude-producer): install must REPLACE its leftover hook/statusLine
// entries rather than leaving them to double-fire alongside the new ones, and
// uninstall must remove them too.

const legacyHookSettings = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/bin/awtrix-claude-producer hook stop"}]}]}}` + "\n"

func TestMergeSettings_ReplacesLegacyAwtrixHooks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacyHookSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(body), "awtrix-claude-producer") {
		t.Errorf("upgrade left the legacy awtrix-claude-producer hook (would double-fire):\n%s", body)
	}
	if !strings.Contains(string(body), "ember-claude-producer hook stop") {
		t.Errorf("new ember hook missing after upgrade:\n%s", body)
	}
}

func TestUninstall_RemovesLegacyAwtrixHooks(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacyHookSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := uninstallSettings(tmp); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(body), "awtrix-claude-producer") {
		t.Errorf("uninstall left a legacy awtrix-claude-producer hook:\n%s", body)
	}
}

func TestStatusLineIsOurs_LegacyAwtrix(t *testing.T) {
	old := map[string]any{
		"type":    "command",
		"command": "/old/bin/awtrix-claude-producer statusline 2>>$HOME/Library/Logs/awtrix-claude-producer.log",
	}
	if !statusLineIsOurs(old) {
		t.Error("statusLineIsOurs should recognize the legacy awtrix-claude-producer statusline so upgrade reclaims (not captures) the slot")
	}
}
