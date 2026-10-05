package producer

import (
	"log/slog"
	"testing"
)

func TestBool(t *testing.T) {
	for _, tc := range []struct {
		v        string
		def, out bool
	}{
		{"true", false, true}, {"ON", false, true}, {"1", false, true}, {"Yes", false, true},
		{"false", true, false}, {"Off", true, false}, {"0", true, false}, {"no", true, false},
		{"", true, true}, {"", false, false}, {"maybe", true, true}, {"maybe", false, false},
	} {
		if got := Bool(tc.v, tc.def); got != tc.out {
			t.Errorf("Bool(%q, %v) = %v, want %v", tc.v, tc.def, got, tc.out)
		}
	}
}

func TestCommonSetAndStatusRequest(t *testing.T) {
	c := DefaultCommon()
	for k, v := range map[string]string{
		"EMBER_SOURCE": "mbp", "EMBER_SOURCE_COLOR": "#ff0000", "EMBER_TOKEN": "tok",
		"EMBER_SERVER_URL": "http://h:1", "EMBER_SERVER_INSTANCE": "home",
		"EMBER_SOURCE_CARD": "off", "EMBER_SESSION_BAR": "", "EMBER_ACTIVITY_TRAIL_ENABLED": "0",
	} {
		if !c.Set(k, v) {
			t.Fatalf("%s not a Common key", k)
		}
	}
	if c.Set("EMBER_RATE_RESET", "on") {
		t.Fatal("EMBER_RATE_RESET is a Gauges key")
	}
	if c.SourceCardEnabled || !c.SessionBarEnabled || c.ActivityTrailEnabled || c.ServerInstance != "home" {
		t.Fatalf("toggles = %+v", c)
	}
	req := c.StatusRequest("codex", "s1", "running")
	if req.Source != "mbp" || req.Tool != "codex" || req.Session != "s1" || req.State != "running" {
		t.Fatalf("req = %+v", req)
	}
	if req.SourceColor == nil || *req.SourceColor != "#ff0000" || *req.SourceCard || !*req.SessionBar {
		t.Fatalf("card fields = %v %v %v", req.SourceColor, *req.SourceCard, *req.SessionBar)
	}
	c.SourceColor = ""
	if c.StatusRequest("t3", "x", "idle").SourceColor != nil {
		t.Fatal("empty source color must stay unset")
	}
	for _, a := range c.LogAttrs() {
		if a.Key == "token" && a.Value.String() != "set" {
			t.Fatalf("token attr = %v", a.Value)
		}
	}
}

func TestCommonResolve(t *testing.T) {
	t.Setenv("EMBER_TOKEN", "from-env")
	c := Common{Source: "mbp", ServerURL: "http://h:1"}
	c.Resolve(t.TempDir())
	if c.ServerConfigured != "http://h:1" || c.ServerURL != "http://h:1" || c.ServerAuto || c.Token != "from-env" {
		t.Fatalf("resolved = %+v", c)
	}
	c = Common{Source: "mbp", Token: "file"}
	c.Resolve(t.TempDir())
	if c.Token != "file" || !c.ServerAuto {
		t.Fatalf("resolved = %+v", c)
	}
}

func TestGauges(t *testing.T) {
	g := DefaultGauges()
	for k, v := range map[string]string{
		"EMBER_CONTEXT_PCT_ENABLED": "no", "EMBER_CONTEXT_NUMBER_ENABLED": "yes",
		"EMBER_RATE_BOTTOM_BAR": "1", "EMBER_RATE_RESET": "",
	} {
		if !g.Set(k, v) {
			t.Fatalf("%s not a Gauges key", k)
		}
	}
	if g.Set("EMBER_SOURCE", "x") {
		t.Fatal("EMBER_SOURCE is not a Gauges key")
	}
	var req StatusRequest
	g.Apply(&req)
	if g.ContextPctEnabled || !req.ContextNumber || !req.RateBottomBar || req.RateReset {
		t.Fatalf("gauges = %+v req = %+v", g, req)
	}
	if n := len(slog.GroupValue(g.LogAttrs()...).Group()); n != 4 {
		t.Fatalf("log attrs = %d", n)
	}
}
