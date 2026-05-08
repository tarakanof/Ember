package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
