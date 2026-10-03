package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Turning Ember off (deconfigure/uninstall, Ember.app's Agents toggle) must
// silence the hooks even when the plugin, which deconfigure can't remove,
// still registers them.
func TestDeconfigureDisablesHooks_ConfigureReenables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !hooksEnabledAt(home) {
		t.Fatal("hooks must run on a fresh machine (plugin-only setups never run configure)")
	}
	if err := configureAt(home, testBin); err != nil {
		t.Fatal(err)
	}
	if err := deconfigureAt(home); err != nil {
		t.Fatal(err)
	}
	if hooksEnabledAt(home) {
		t.Fatal("deconfigure must disable hooks")
	}
	if err := configureAt(home, testBin); err != nil {
		t.Fatal(err)
	}
	if !hooksEnabledAt(home) {
		t.Fatal("configure must re-enable hooks")
	}
}

func TestDeconfigure_NoConfigDirStillDisables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := deconfigureAt(home); err != nil {
		t.Fatal(err)
	}
	if hooksEnabledAt(home) {
		t.Fatal("deconfigure without a prior configure must still disable hooks")
	}
	if _, err := os.Stat(hooksDisabledPath(home)); err != nil {
		t.Fatal(err)
	}
}

func TestPluginStillEnabledWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if w := pluginStillEnabledWarning(home); w != "" {
		t.Errorf("no plugin: want no warning, got %q", w)
	}
	writeSettings(t, home, map[string]any{"enabledPlugins": map[string]any{pluginID: true}})
	w := pluginStillEnabledWarning(home)
	if !strings.Contains(w, "claude plugin disable "+pluginID) {
		t.Errorf("warning %q should tell the user to disable the plugin", w)
	}
}

func TestHookSubcommand_KillSwitchSkipsBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".config", "ember"))
	env := "EMBER_SOURCE=test\nEMBER_SERVER_URL=" + srv.URL + "\nEMBER_TOKEN=x\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "ember", "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ecp")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	run := func() {
		cmd := exec.Command(bin, "hook", "user-prompt-submit")
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","cwd":"/r","prompt":"hi"}`)
		if err := cmd.Run(); err != nil {
			t.Fatalf("hook must exit 0: %v", err)
		}
	}

	if err := os.WriteFile(hooksDisabledPath(home), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	if n := hits.Load(); n != 0 {
		t.Fatalf("disabled hooks reached the server %d times", n)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "ember", "sessions", "s1.json")); !os.IsNotExist(err) {
		t.Fatalf("disabled hooks must not write markers (nothing would reap them after uninstall)")
	}

	if err := os.Remove(hooksDisabledPath(home)); err != nil {
		t.Fatal(err)
	}
	run()
	if n := hits.Load(); n != 1 {
		t.Fatalf("enabled hook: %d requests, want 1", n)
	}
}
