package main

import (
	"encoding/json"
	"os"
	"testing"
)

func ratePtr(i int) *int { return &i }

func TestExtractRatePct(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{"present", `{"rate_limits":{"five_hour":{"used_percentage":62.4}}}`, ratePtr(62)},
		{"rounds up", `{"rate_limits":{"five_hour":{"used_percentage":62.6}}}`, ratePtr(63)},
		{"clamp high", `{"rate_limits":{"five_hour":{"used_percentage":150}}}`, ratePtr(100)},
		{"zero", `{"rate_limits":{"five_hour":{"used_percentage":0}}}`, ratePtr(0)},
		{"no five_hour", `{"rate_limits":{}}`, nil},
		{"no rate_limits", `{"session_id":"x"}`, nil},
	}
	for _, c := range cases {
		in, ok := parseStatusline([]byte(c.json))
		if !ok {
			t.Fatalf("%s: parse failed", c.name)
		}
		got, gotOK := extractRatePct(in)
		if c.want == nil {
			if gotOK {
				t.Errorf("%s: want none, got %d", c.name, *got)
			}
			continue
		}
		if !gotOK || got == nil || *got != *c.want {
			t.Errorf("%s: got %v (ok=%v), want %d", c.name, got, gotOK, *c.want)
		}
	}
	if _, ok := parseStatusline([]byte("not json")); ok {
		t.Error("malformed JSON should parse false")
	}
}

func TestEnrichMarkerRate(t *testing.T) {
	dir := t.TempDir()

	mp := markerPath(dir, "sess1")
	if err := os.WriteFile(mp, []byte(`{"source":"mbp","tool":"claude","session":"sess1","state":"running","message":"Bash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enrichMarkerRate(dir, "sess1", 73); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(mp)
	var req StatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.RateWindowPct == nil || *req.RateWindowPct != 73 {
		t.Errorf("rate_window_pct = %v, want 73", req.RateWindowPct)
	}
	if req.State != "running" || req.Source != "mbp" || req.Message != "Bash" {
		t.Errorf("hook fields not preserved: %+v", req)
	}

	if err := enrichMarkerRate(dir, "ghost", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(markerPath(dir, "ghost")); !os.IsNotExist(err) {
		t.Error("ghost marker should not be created")
	}

	bad := markerPath(dir, "bad")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enrichMarkerRate(dir, "bad", 50); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bad); string(got) != "not json" {
		t.Errorf("unparseable marker modified: %q", got)
	}
}

func TestReadWrappedCommand(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/w.json"

	os.WriteFile(p, []byte(`"~/.claude/sl.sh"`), 0o600)
	if c, ok := readWrappedCommand(p); !ok || c != "~/.claude/sl.sh" {
		t.Errorf("string form: %q ok=%v", c, ok)
	}
	os.WriteFile(p, []byte(`{"type":"command","command":"foo.sh","padding":2}`), 0o600)
	if c, ok := readWrappedCommand(p); !ok || c != "foo.sh" {
		t.Errorf("object form: %q ok=%v", c, ok)
	}
	if _, ok := readWrappedCommand(dir + "/nope.json"); ok {
		t.Error("missing sidecar should be (.,false)")
	}
	os.WriteFile(p, []byte(`{"type":"command"}`), 0o600)
	if _, ok := readWrappedCommand(p); ok {
		t.Error("object without command should be (.,false)")
	}
}

func TestRunWrapped(t *testing.T) {
	out, err := runWrapped("cat", []byte("hello-json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello-json" {
		t.Errorf("runWrapped(cat) = %q, want hello-json", out)
	}
}

func TestStatusLineIsOurs(t *testing.T) {
	ours := map[string]any{"type": "command", "command": "/x/awtrix-claude-producer statusline 2>>y"}
	if !statusLineIsOurs(ours) {
		t.Error("our command should be detected as ours")
	}
	if statusLineIsOurs("~/.claude/sl.sh") {
		t.Error("user command should not be detected as ours")
	}
}
