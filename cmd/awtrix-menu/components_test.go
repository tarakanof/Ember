package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlistPathAndTarget(t *testing.T) {
	if got := agentPlistPath("/Users/joe", "com.awtrix-ai-status.codex"); got != "/Users/joe/Library/LaunchAgents/com.awtrix-ai-status.codex.plist" {
		t.Errorf("agentPlistPath = %q", got)
	}
	if got := guiTarget(501, "com.x"); got != "gui/501/com.x" {
		t.Errorf("guiTarget = %q", got)
	}
}

func TestPlanToggle(t *testing.T) {
	menu := component{label: "com.awtrix-ai-status.menu", binary: ""}
	claude := component{label: "com.awtrix-ai-status.heartbeat", binary: "awtrix-claude-producer"}

	// Install a not-installed producer.
	ops := planToggle(claude, componentState{}, true, "/bin/p")
	if len(ops) != 1 || ops[0].bin != "/bin/p" || ops[0].args[0] != "install" {
		t.Errorf("install expected, got %v", ops)
	}
	// Uninstall an installed producer.
	ops = planToggle(claude, componentState{Installed: true}, false, "/bin/p")
	if len(ops) != 1 || ops[0].bin != "/bin/p" || ops[0].args[0] != "uninstall" {
		t.Errorf("uninstall expected, got %v", ops)
	}
	// No-op: already installed and want install.
	if ops := planToggle(claude, componentState{Installed: true}, true, "/bin/p"); ops != nil {
		t.Errorf("no-op expected, got %v", ops)
	}
	// No-op: not installed and want uninstall.
	if ops := planToggle(claude, componentState{}, false, "/bin/p"); ops != nil {
		t.Errorf("no-op expected, got %v", ops)
	}
	// Menu row is read-only: never plans an op.
	if ops := planToggle(menu, componentState{}, true, "/bin/p"); ops != nil {
		t.Errorf("menu row must be read-only, got %v", ops)
	}
	if ops := planToggle(menu, componentState{Installed: true}, false, "/bin/p"); ops != nil {
		t.Errorf("menu row must be read-only, got %v", ops)
	}
}

func TestResolveBinary(t *testing.T) {
	home := t.TempDir()
	selfDir := t.TempDir()

	lookHit := func(string) (string, error) { return "/usr/local/bin/awtrix-claude-producer", nil }
	if got := resolveBinary("awtrix-claude-producer", lookHit, home, selfDir); got != "/usr/local/bin/awtrix-claude-producer" {
		t.Errorf("PATH hit: got %q", got)
	}

	lookMiss := func(string) (string, error) { return "", os.ErrNotExist }
	gobin := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(gobin, "awtrix-codex-producer")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveBinary("awtrix-codex-producer", lookMiss, home, selfDir); got != bin {
		t.Errorf("go/bin fallback: got %q want %q", got, bin)
	}

	if got := resolveBinary("nope", lookMiss, home, selfDir); got != "" {
		t.Errorf("not found: got %q want \"\"", got)
	}
}

func TestComponentState_Derived(t *testing.T) {
	cases := []struct {
		name      string
		st        componentState
		wantLogin bool
		wantLabel string
	}{
		{"not installed", componentState{}, false, "Not installed"},
		{"installed running", componentState{Installed: true, Loaded: true}, true, "On · running"},
		{"installed stopped", componentState{Installed: true}, true, "On · stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.st.launchAtLogin(); got != c.wantLogin {
				t.Errorf("launchAtLogin = %v, want %v", got, c.wantLogin)
			}
			if got := c.st.stateLabel(); got != c.wantLabel {
				t.Errorf("stateLabel = %q, want %q", got, c.wantLabel)
			}
		})
	}
}
