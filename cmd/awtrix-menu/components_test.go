package main

import "testing"

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
