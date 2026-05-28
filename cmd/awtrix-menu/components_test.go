package main

import "testing"

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
