package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsBackups(t *testing.T, home string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(home, ".claude", "settings.json.bak.*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMergeSettingsKeepsOneBackupAndLegacyFiles(t *testing.T) {
	home := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(sp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := []string{"settings.json.bak.1", "settings.json.bak.20260105", "settings.json.bak.keep-me"}
	for _, name := range legacy {
		if err := os.WriteFile(filepath.Join(home, ".claude", name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	if err := uninstallSettings(home); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	for _, name := range legacy {
		if _, err := os.Stat(filepath.Join(home, ".claude", name)); err != nil {
			t.Errorf("deleted %s", name)
		}
	}
	if n := legacySettingsBackups(sp); n != 2 {
		t.Errorf("legacy backups = %d, want 2", n)
	}
	if baks := settingsBackups(t, home); len(baks) != 3 {
		t.Errorf("configure added .bak.* files: %v", baks)
	}
	b, err := os.ReadFile(settingsBackupPath(sp))
	if err != nil || !strings.Contains(string(b), `"model": "opus"`) {
		t.Fatalf("backup = %q, %v (want the pre-configure file from the last run)", b, err)
	}
}

func TestUninstallSettingsBackupFailureOnlyWarns(t *testing.T) {
	home := t.TempDir()
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(home, ".claude", "settings.json")
	_ = os.Remove(settingsBackupPath(sp))
	if err := os.Mkdir(settingsBackupPath(sp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := uninstallSettings(home); err != nil {
		t.Fatalf("uninstall blocked by a failed backup: %v", err)
	}
	if b, _ := os.ReadFile(sp); strings.Contains(string(b), "ember-claude-producer") {
		t.Fatalf("hooks not removed: %s", b)
	}
	if err := os.WriteFile(sp, []byte(`{"model":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err == nil {
		t.Fatal("configure must not rewrite settings.json without a backup")
	}
}

func TestMergeSettingsNoopWritesNothing(t *testing.T) {
	home := t.TempDir()
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(home, ".claude", "settings.json")
	before, _ := os.Stat(sp)
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(sp)
	if !os.SameFile(before, after) {
		t.Fatal("an unchanged settings.json was rewritten")
	}
	if _, err := os.Stat(settingsBackupPath(sp)); !os.IsNotExist(err) {
		t.Fatal("a configure on a fresh home left a backup")
	}
}

func TestSettingsSymlinkPreserved(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "dotfiles", "claude-settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(sp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, sp); err != nil {
		t.Fatal(err)
	}
	check := func(step string) {
		t.Helper()
		fi, err := os.Lstat(sp)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s: settings.json is no longer a symlink", step)
		}
	}
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	check("configure")
	if b, _ := os.ReadFile(target); !bytesContains(b, "ember-claude-producer statusline") {
		t.Fatalf("link target not updated: %s", b)
	}
	if err := uninstallSettings(home); err != nil {
		t.Fatal(err)
	}
	check("deconfigure")
	if b, _ := os.ReadFile(target); bytesContains(b, "ember-claude-producer") {
		t.Fatalf("deconfigure left producer entries in the target: %s", b)
	}
}

func bytesContains(b []byte, s string) bool { return strings.Contains(string(b), s) }
