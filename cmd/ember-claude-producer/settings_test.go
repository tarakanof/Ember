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

func TestMergeSettingsKeepsOneBackup(t *testing.T) {
	home := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(sp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Backups an older configure run left behind, plus an unrelated file.
	for _, name := range []string{"settings.json.bak.1", "settings.json.bak.22", "settings.json.bak.keep-me"} {
		if err := os.WriteFile(filepath.Join(home, ".claude", name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := mergeSettingsJSON(home, "/bin/ember-claude-producer"); err != nil {
		t.Fatal(err)
	}
	baks := settingsBackups(t, home)
	if len(baks) != 2 { // ours + the non-numeric one
		t.Fatalf("backups = %v, want this run's plus keep-me", baks)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json.bak.keep-me")); err != nil {
		t.Fatal("pruned a file that is not a pid backup")
	}
	if b, _ := os.ReadFile(sp + ".bak." + itoa(os.Getpid())); string(b) != `{"model":"opus"}` {
		t.Fatalf("backup = %q", b)
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
	if baks := settingsBackups(t, home); len(baks) != 0 {
		t.Fatalf("no-op configure left backups %v", baks)
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
