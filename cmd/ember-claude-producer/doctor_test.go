package main

import "testing"

// Representative `launchctl print gui/<uid>/<label>` output for a loaded,
// healthy periodic agent (between ticks: not currently running, last run OK).
const sampleLaunchctlPrint = `com.ember.heartbeat = {
	active count = 0
	path = /Users/joe/Library/LaunchAgents/com.ember.heartbeat.plist
	state = not running
	program = /Users/joe/go/bin/ember-claude-producer
	runs = 7
	last exit code = 0
}`

func TestLaunchctlField(t *testing.T) {
	cases := []struct {
		key, want string
	}{
		{"runs", "7"},
		{"last exit code", "0"},
		{"state", "not running"},
		{"program", "/Users/joe/go/bin/ember-claude-producer"},
		{"missing", ""},
	}
	for _, c := range cases {
		if got := launchctlField(sampleLaunchctlPrint, c.key); got != c.want {
			t.Errorf("launchctlField(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHeartbeatStatusLine_NotLoaded(t *testing.T) {
	hint := "launchctl bootstrap gui/501 /path/to.plist"
	got := heartbeatStatusLine(false, "", hint)
	if !contains(got, "NOT loaded") {
		t.Errorf("not-loaded line should say NOT loaded, got %q", got)
	}
	if !contains(got, hint) {
		t.Errorf("not-loaded line should include the bootstrap hint, got %q", got)
	}
}

func TestHeartbeatStatusLine_LoadedSurfacesRunsAndExit(t *testing.T) {
	got := heartbeatStatusLine(true, sampleLaunchctlPrint, "irrelevant")
	want := "loaded (runs=7, last exit=0)"
	if got != want {
		t.Errorf("loaded line = %q, want %q", got, want)
	}
}

func TestHeartbeatStatusLine_LoadedNoFields(t *testing.T) {
	if got := heartbeatStatusLine(true, "", "irrelevant"); got != "loaded" {
		t.Errorf("loaded line with no parseable fields = %q, want %q", got, "loaded")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
