package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestExtractContextPct(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{"present", `{"context_window":{"used_percentage":54.4}}`, ratePtr(54)},
		{"rounds up", `{"context_window":{"used_percentage":54.6}}`, ratePtr(55)},
		{"clamp high", `{"context_window":{"used_percentage":250}}`, ratePtr(100)},
		{"zero", `{"context_window":{"used_percentage":0}}`, ratePtr(0)},
		{"absent", `{"session_id":"x"}`, nil},
	}
	for _, c := range cases {
		in, ok := parseStatusline([]byte(c.json))
		if !ok {
			t.Fatalf("%s: parse failed", c.name)
		}
		got, gotOK := extractContextPct(in)
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
}

func TestExtractWeekPct(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{"present", `{"rate_limits":{"seven_day":{"used_percentage":40.4}}}`, ratePtr(40)},
		{"rounds up", `{"rate_limits":{"seven_day":{"used_percentage":40.6}}}`, ratePtr(41)},
		{"clamp high", `{"rate_limits":{"seven_day":{"used_percentage":150}}}`, ratePtr(100)},
		{"zero", `{"rate_limits":{"seven_day":{"used_percentage":0}}}`, ratePtr(0)},
		{"no seven_day", `{"rate_limits":{"five_hour":{"used_percentage":10}}}`, nil},
		{"no rate_limits", `{"session_id":"x"}`, nil},
	}
	for _, c := range cases {
		in, ok := parseStatusline([]byte(c.json))
		if !ok {
			t.Fatalf("%s: parse failed", c.name)
		}
		got, gotOK := extractWeekPct(in)
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
	if _, ok := extractWeekPct(statuslineInput{}); ok {
		t.Error("empty input should report ok=false")
	}
}

// A malformed rate_limits.seven_day (wrong type) must not parse into a usable
// value — parseStatusline should either fail outright or leave SevenDay nil,
// and extractWeekPct must report ok=false either way.
func TestExtractWeekPct_Malformed(t *testing.T) {
	if in, ok := parseStatusline([]byte(`{"rate_limits":{"seven_day":"not-an-object"}}`)); ok {
		if _, gotOK := extractWeekPct(in); gotOK {
			t.Error("malformed seven_day should not yield a value")
		}
	}
}

func TestExtractWeekResetAt(t *testing.T) {
	in, _ := parseStatusline([]byte(`{"rate_limits":{"seven_day":{"used_percentage":20,"resets_at":1778700000}}}`))
	got, ok := extractWeekResetAt(in)
	if !ok || got != 1778700000 {
		t.Errorf("extractWeekResetAt = (%d,%v), want (1778700000,true)", got, ok)
	}
	none, _ := parseStatusline([]byte(`{"rate_limits":{"seven_day":{"used_percentage":20}}}`))
	if _, ok := extractWeekResetAt(none); ok {
		t.Error("absent resets_at should report ok=false")
	}
}

func TestExtractWeekResetLabel(t *testing.T) {
	// 1778700000 unix -> a specific instant; just assert it round-trips through
	// dayLabel the same way usage.go's poller path would for the same instant.
	in, _ := parseStatusline([]byte(`{"rate_limits":{"seven_day":{"used_percentage":20,"resets_at":1778700000}}}`))
	got, ok := extractWeekResetLabel(in)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := dayLabel(time.Unix(1778700000, 0), time.Local)
	if got != want {
		t.Errorf("extractWeekResetLabel = %q, want %q", got, want)
	}
	none, _ := parseStatusline([]byte(`{"rate_limits":{"five_hour":{"used_percentage":20}}}`))
	if _, ok := extractWeekResetLabel(none); ok {
		t.Error("absent seven_day should report ok=false")
	}
}

func TestExtractRateResetAt(t *testing.T) {
	in, _ := parseStatusline([]byte(`{"rate_limits":{"five_hour":{"used_percentage":20,"resets_at":1778614633}}}`))
	got, ok := extractRateResetAt(in)
	if !ok || got != 1778614633 {
		t.Errorf("extractRateResetAt = (%d,%v), want (1778614633,true)", got, ok)
	}
	none, _ := parseStatusline([]byte(`{"rate_limits":{"five_hour":{"used_percentage":20}}}`))
	if _, ok := extractRateResetAt(none); ok {
		t.Error("absent resets_at should report ok=false")
	}
}

func TestEnrichMarker_SetsAndPreservesResetAt(t *testing.T) {
	dir := t.TempDir()
	// seed a marker via a hook upsert
	markerP := markerPath(dir, "sess1")
	lockP := lockPath(dir, "sess1")
	cfg := Config{Source: "mbp", ServerURL: "http://x"}
	handleUpsert(context.Background(), cfg, NewClient(cfg), "sess1", "running", "m", "", markerP, lockP)
	ra := int64(1778614633)
	wra := int64(1778700000)
	if err := enrichMarker(dir, "sess1", ratePtr(50), nil, &ra, "14:25", ratePtr(30), &wra, "MON"); err != nil {
		t.Fatal(err)
	}
	raw, _ := readMarker(markerP)
	var req StatusRequest
	_ = json.Unmarshal(raw, &req)
	if req.RateResetAt != 1778614633 {
		t.Errorf("RateResetAt = %d, want 1778614633", req.RateResetAt)
	}
	if req.RateResetLabel != "14:25" {
		t.Errorf("RateResetLabel = %q, want 14:25", req.RateResetLabel)
	}
	if req.RateWeekPct == nil || *req.RateWeekPct != 30 {
		t.Errorf("RateWeekPct = %v, want 30", req.RateWeekPct)
	}
	if req.RateWeekResetAt != 1778700000 {
		t.Errorf("RateWeekResetAt = %d, want 1778700000", req.RateWeekResetAt)
	}
	if req.RateWeekResetLabel != "MON" {
		t.Errorf("RateWeekResetLabel = %q, want MON", req.RateWeekResetLabel)
	}
	// A subsequent hook upsert must PRESERVE the statusline-owned reset + label.
	handleUpsert(context.Background(), cfg, NewClient(cfg), "sess1", "running", "m2", "", markerP, lockP)
	raw2, _ := readMarker(markerP)
	var req2 StatusRequest
	_ = json.Unmarshal(raw2, &req2)
	if req2.RateResetAt != 1778614633 {
		t.Errorf("hook clobbered RateResetAt: got %d", req2.RateResetAt)
	}
	if req2.RateResetLabel != "14:25" {
		t.Errorf("hook clobbered RateResetLabel: got %q", req2.RateResetLabel)
	}
}

// The statusline runs often; it must not strip the owner-liveness fields the
// hook recorded, or the heartbeat could never detect an ungraceful close.
func TestEnrichMarker_PreservesOwner(t *testing.T) {
	dir := t.TempDir()
	mp := markerPath(dir, "sess1")
	seed := marker{
		StatusRequest: StatusRequest{Source: "mbp", Tool: "claude", Session: "sess1", State: "running"},
		OwnerPID:      12345,
		OwnerStart:    "Wed May 28 10:00:00 2026",
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(mp, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enrichMarker(dir, "sess1", ratePtr(50), nil, nil, "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := readMarker(mp)
	var got marker
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.OwnerPID != 12345 || got.OwnerStart != "Wed May 28 10:00:00 2026" {
		t.Errorf("statusline stripped owner: pid=%d start=%q", got.OwnerPID, got.OwnerStart)
	}
	if got.RateWindowPct == nil || *got.RateWindowPct != 50 {
		t.Errorf("enrich did not apply rate_window_pct")
	}
}

func TestContextPctEnabled(t *testing.T) {
	write := func(t *testing.T, body string) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".config", "ember")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "producer.env"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(t, "EMBER_SOURCE=mbp\n")
	if !contextPctEnabled() {
		t.Error("default (absent key) should be true")
	}
	write(t, "EMBER_SOURCE=mbp\nEMBER_CONTEXT_PCT_ENABLED=off\n")
	if contextPctEnabled() {
		t.Error("=off should be false")
	}
}

func TestEnrichMarker(t *testing.T) {
	dir := t.TempDir()

	mp := markerPath(dir, "sess1")
	if err := os.WriteFile(mp, []byte(`{"source":"mbp","tool":"claude","session":"sess1","state":"running","message":"Bash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enrichMarker(dir, "sess1", ratePtr(73), ratePtr(54), nil, "", nil, nil, ""); err != nil {
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
	if req.ContextPct == nil || *req.ContextPct != 54 {
		t.Errorf("context_pct = %v, want 54", req.ContextPct)
	}
	if req.State != "running" || req.Source != "mbp" || req.Message != "Bash" {
		t.Errorf("hook fields not preserved: %+v", req)
	}

	// nil ctx leaves context_pct untouched (rate-only enrichment).
	if err := enrichMarker(dir, "sess1", ratePtr(80), nil, nil, "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(mp)
	req = StatusRequest{}
	_ = json.Unmarshal(body, &req)
	if req.ContextPct == nil || *req.ContextPct != 54 {
		t.Errorf("nil ctx should leave context_pct=54, got %v", req.ContextPct)
	}
	if req.RateWindowPct == nil || *req.RateWindowPct != 80 {
		t.Errorf("rate should update to 80, got %v", req.RateWindowPct)
	}

	// Absent marker → not created.
	if err := enrichMarker(dir, "ghost", ratePtr(50), ratePtr(50), nil, "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(markerPath(dir, "ghost")); !os.IsNotExist(err) {
		t.Error("ghost marker should not be created")
	}

	// Unparseable marker → untouched.
	bad := markerPath(dir, "bad")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enrichMarker(dir, "bad", ratePtr(50), ratePtr(50), nil, "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bad); string(got) != "not json" {
		t.Errorf("unparseable marker modified: %q", got)
	}
}

// TestEnrichMarker_Week covers the seven_day (weekly) fields: set, then a
// nil weekPct on a later enrich call must leave the previous value untouched
// (same "never clears" contract as the five-hour and context fields).
func TestEnrichMarker_Week(t *testing.T) {
	dir := t.TempDir()
	mp := markerPath(dir, "sess1")
	if err := os.WriteFile(mp, []byte(`{"source":"mbp","tool":"claude","session":"sess1","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	wra := int64(1778700000)
	if err := enrichMarker(dir, "sess1", nil, nil, nil, "", ratePtr(35), &wra, "MON"); err != nil {
		t.Fatal(err)
	}
	var req StatusRequest
	body, _ := os.ReadFile(mp)
	_ = json.Unmarshal(body, &req)
	if req.RateWeekPct == nil || *req.RateWeekPct != 35 {
		t.Errorf("rate_week_pct = %v, want 35", req.RateWeekPct)
	}
	if req.RateWeekResetAt != 1778700000 {
		t.Errorf("rate_week_reset_at = %d, want 1778700000", req.RateWeekResetAt)
	}
	if req.RateWeekResetLabel != "MON" {
		t.Errorf("rate_week_reset_label = %q, want MON", req.RateWeekResetLabel)
	}

	// A later enrich call with weekPct=nil (e.g. seven_day absent from a
	// statusline payload) must NOT clear the previously stored value.
	if err := enrichMarker(dir, "sess1", ratePtr(10), nil, nil, "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	req = StatusRequest{}
	body, _ = os.ReadFile(mp)
	_ = json.Unmarshal(body, &req)
	if req.RateWeekPct == nil || *req.RateWeekPct != 35 {
		t.Errorf("nil weekPct should leave rate_week_pct=35, got %v", req.RateWeekPct)
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
	ours := map[string]any{"type": "command", "command": "/x/ember-claude-producer statusline 2>>y"}
	if !statusLineIsOurs(ours) {
		t.Error("our command should be detected as ours")
	}
	if statusLineIsOurs("~/.claude/sl.sh") {
		t.Error("user command should not be detected as ours")
	}
}
