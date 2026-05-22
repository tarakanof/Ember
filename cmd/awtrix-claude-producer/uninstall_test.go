package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallSettings_RestoresStatusLine(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "awtrix-ai-status"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	sidecar := filepath.Join(home, ".config", "awtrix-ai-status", "wrapped-statusline.json")

	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/awtrix-claude-producer statusline 2>>y"}}`), 0o600)
	os.WriteFile(sidecar, []byte(`{"type":"command","command":"mine.sh","padding":2}`), 0o600)

	if err := uninstallSettings(home); err != nil {
		t.Fatal(err)
	}
	sb, _ := os.ReadFile(settings)
	if !strings.Contains(string(sb), "mine.sh") || strings.Contains(string(sb), "awtrix-claude-producer statusline") {
		t.Errorf("statusLine not restored to user's: %s", sb)
	}
	if !strings.Contains(string(sb), "padding") {
		t.Errorf("object-form extras (padding) lost on restore: %s", sb)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Error("sidecar should be removed after restore")
	}
}

func TestUninstallSettings_RemovesStatusLineWhenNoSidecar(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	settings := filepath.Join(home, ".claude", "settings.json")
	// No hooks at all — exercises the no-early-return fix.
	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/awtrix-claude-producer statusline 2>>y"}}`), 0o600)

	if err := uninstallSettings(home); err != nil {
		t.Fatal(err)
	}
	sb, _ := os.ReadFile(settings)
	if strings.Contains(string(sb), "statusLine") {
		t.Errorf("statusLine key should be removed when no sidecar: %s", sb)
	}
}

func TestUninstall_StripsHooksAndRemovesPlist(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := mergeSettingsJSON(tmp, "/usr/local/bin/awtrix-claude-producer"); err != nil {
		t.Fatal(err)
	}
	plistDir := filepath.Join(tmp, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plistP := filepath.Join(plistDir, "com.awtrix-ai-status.heartbeat.plist")
	if err := os.WriteFile(plistP, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := uninstallSettings(tmp); err != nil {
		t.Fatal(err)
	}
	if err := uninstallPlist(tmp, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(tmp, ".claude", "settings.json"))
	if strings.Contains(string(body), "awtrix-claude-producer") {
		t.Errorf("settings still contain awtrix entries: %s", body)
	}
	if _, err := os.Stat(plistP); !os.IsNotExist(err) {
		t.Errorf("plist not removed")
	}
}
