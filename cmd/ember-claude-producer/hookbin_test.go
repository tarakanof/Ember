package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBinary(t *testing.T, path, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho ember-claude-producer " + version + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHookBinaryLines_AppFirstWarnsAndListsOtherCopies(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Apps", "Ember.app", "Contents", "MacOS", producerName)
	goBin := filepath.Join(home, "go", "bin", producerName)
	localBin := filepath.Join(home, ".local", "bin", producerName)
	fakeBinary(t, app, "0.94.0")
	fakeBinary(t, goBin, "0.42.0")
	fakeBinary(t, localBin, "0.43.0")
	e := hookBinaryEnv{home: home, appDirs: []string{filepath.Join(home, "Apps")}}
	lines := strings.Join(hookBinaryLines(e, goBin), "\n")
	for _, want := range []string{"hook binary: " + app + " (0.94.0)", "WARNING: the hooks run " + app, "hint: " + localBin + " (0.43.0) is another copy the hooks do not use"} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q in:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "hint: "+goBin) {
		t.Errorf("no delete hint for the binary running doctor:\n%s", lines)
	}
}

func TestHookBinaryLines_OldInstalledShimRunsPathFirst(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Apps", "Ember.app", "Contents", "MacOS", producerName)
	goBin := filepath.Join(home, "go", "bin", producerName)
	fakeBinary(t, app, "0.94.0")
	fakeBinary(t, goBin, "0.42.0")
	installFakePlugin(t, home, "{}", "#!/bin/sh\nbin=$(command -v x)\nfor d in a; do $d/Ember.app/Contents/MacOS/x; done\n")
	t.Setenv("EMBER_APP_DIRS", filepath.Join(home, "Apps"))
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("EMBER_CLAUDE_PRODUCER", "")
	os.Unsetenv("EMBER_CLAUDE_PRODUCER")
	lines := strings.Join(hookBinaryLines(hookBinaryEnvFromOS(home), app), "\n")
	if !strings.Contains(lines, "hook binary: "+goBin+" (0.42.0)") {
		t.Fatalf("the old shim runs ~/go/bin first, got:\n%s", lines)
	}
	if strings.Contains(lines, "hint: "+app) {
		t.Fatalf("doctor must never suggest deleting the app's helper:\n%s", lines)
	}
	if !strings.Contains(lines, "WARNING: the hooks run "+goBin) {
		t.Fatalf("missing warning:\n%s", lines)
	}
}

func TestHookBinaryLines_SameBinaryIsQuiet(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Apps", "Ember.app", "Contents", "MacOS", producerName)
	fakeBinary(t, app, "0.94.0")
	e := hookBinaryEnv{home: home, appDirs: []string{filepath.Join(home, "Apps")}}
	lines := hookBinaryLines(e, app)
	if len(lines) != 1 || strings.Contains(lines[0], "WARNING") {
		t.Fatalf("got %v", lines)
	}
}

func TestHookBinaryLines_OverrideWinsAlone(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Apps", "Ember.app", "Contents", "MacOS", producerName)
	custom := filepath.Join(home, "custom", "ecp")
	fakeBinary(t, app, "0.94.0")
	fakeBinary(t, custom, "dev")
	e := hookBinaryEnv{home: home, override: &custom, appDirs: []string{filepath.Join(home, "Apps")}}
	lines := hookBinaryLines(e, custom)
	if len(lines) != 1 || !strings.Contains(lines[0], custom) {
		t.Fatalf("got %v", lines)
	}
	missing := filepath.Join(home, "nope")
	e.override = &missing
	if l := hookBinaryLines(e, custom); !strings.Contains(l[0], "not an executable") {
		t.Fatalf("got %v", l)
	}
}

func TestHookBinaryLines_NoneFound(t *testing.T) {
	e := hookBinaryEnv{home: t.TempDir()}
	if l := hookBinaryLines(e, ""); len(l) != 1 || !strings.Contains(l[0], "none found") {
		t.Fatalf("got %v", l)
	}
}

func installFakePlugin(t *testing.T, home, hooksJSON, shim string) {
	t.Helper()
	root := filepath.Join(home, ".claude", "plugins", "cache", "ember", "ember", "abc")
	for p, body := range map[string]string{
		filepath.Join(root, "hooks", "hooks.json"):                          hooksJSON,
		filepath.Join(root, "scripts", "ember-hook"):                        shim,
		filepath.Join(home, ".claude", "settings.json"):                     `{"enabledPlugins":{"ember@ember":true}}`,
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"): `{"plugins":{"ember@ember":[{"installPath":"` + root + `"}]}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStalePluginLine(t *testing.T) {
	current, err := os.ReadFile(filepath.Join(pluginDir, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	shim, err := os.ReadFile(filepath.Join(pluginDir, "scripts", "ember-hook"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("current plugin is quiet", func(t *testing.T) {
		home := t.TempDir()
		installFakePlugin(t, home, string(current), string(shim))
		if l := stalePluginLine(home); l != "" {
			t.Fatalf("got %q", l)
		}
	})
	t.Run("old matcher warns with the update commands", func(t *testing.T) {
		home := t.TempDir()
		old := strings.ReplaceAll(string(current), "worker_permission_prompt|", "")
		installFakePlugin(t, home, old, string(shim))
		l := stalePluginLine(home)
		if !strings.Contains(l, "Notification matcher differs") || !strings.Contains(l, "claude plugin update ember@ember") || !strings.Contains(l, "/reload-plugins") {
			t.Fatalf("got %q", l)
		}
	})
	t.Run("old shim order warns", func(t *testing.T) {
		home := t.TempDir()
		installFakePlugin(t, home, string(current), "#!/bin/sh\nbin=$(command -v x)\nfor d in a; do $d/Ember.app/Contents/MacOS/x; done\n")
		if l := stalePluginLine(home); !strings.Contains(l, "prefers PATH over Ember.app") {
			t.Fatalf("got %q", l)
		}
	})
	t.Run("plugin not enabled is quiet", func(t *testing.T) {
		home := t.TempDir()
		installFakePlugin(t, home, "{}", "")
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if l := stalePluginLine(home); l != "" {
			t.Fatalf("got %q", l)
		}
	})
}
