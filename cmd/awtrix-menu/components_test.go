package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestParseDisabled(t *testing.T) {
	out := `disabled services = {
	"com.awtrix-ai-status.codex" => disabled
	"com.awtrix-ai-status.menu" => enabled
	"com.apple.something" => enabled
}`
	m := parseDisabled(out)
	if !m["com.awtrix-ai-status.codex"] {
		t.Error("codex should be disabled")
	}
	if m["com.awtrix-ai-status.menu"] {
		t.Error("menu is listed enabled -> not disabled")
	}
	if m["com.awtrix-ai-status.heartbeat"] {
		t.Error("absent label must default to not-disabled")
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
		{"installed enabled running", componentState{Installed: true, Loaded: true}, true, "Running"},
		{"installed enabled stopped", componentState{Installed: true}, true, "Disabled"},
		{"installed disabled but still running", componentState{Installed: true, Disabled: true, Loaded: true}, false, "Running"},
		{"installed disabled stopped", componentState{Installed: true, Disabled: true}, false, "Disabled"},
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
