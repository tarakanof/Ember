package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Exit(m.Run())
}

const fakeClaudeJSON = `{
	"numStartups": 3,
	"oauthAccount": {
		"accountUuid": "11111111-2222-3333-4444-555555555555",
		"emailAddress": "someone@example.com",
		"organizationUuid": "66666666-7777-8888-9999-000000000000",
		"organizationName": "someone@example.com's Organization",
		"organizationType": "claude_max",
		"organizationRateLimitTier": "default_claude_max_20x"
	}
}`

func writeClaudeJSON(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func resetClaudeAccountCache(t *testing.T) {
	t.Helper()
	reset := func() {
		claudeAccounts.mu.Lock()
		claudeAccounts.sig, claudeAccounts.acct = "", claudeAccount{}
		claudeAccounts.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestClaudeAccountKeyFromFakeConfig(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeJSON(t, home, fakeClaudeJSON)
	a := claudeAccounts.current()
	want := producer.AccountKey("claude", "11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000")
	if a.key != want || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a.key) {
		t.Fatalf("key = %q, want %q", a.key, want)
	}
	if a.plan != "max" {
		t.Fatalf("plan = %q, want max", a.plan)
	}
}

func TestClaudeAccountHonoursClaudeConfigDir(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeJSON(t, home, fakeClaudeJSON)
	alt := filepath.Join(t.TempDir(), "alt")
	writeClaudeJSON(t, alt, `{"oauthAccount":{"accountUuid":"a2","organizationUuid":"o2","organizationType":"claude_pro"}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", alt)
	a := claudeAccounts.current()
	if a.key != producer.AccountKey("claude", "a2", "o2") || a.plan != "pro" {
		t.Fatalf("account = %+v, want the CLAUDE_CONFIG_DIR one on pro", a)
	}
}

func TestClaudeAccountWithoutOAuthIsKeyless(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeJSON(t, home, `{"numStartups": 1}`)
	if a := claudeAccounts.current(); a != (claudeAccount{}) {
		t.Fatalf("account = %+v, want none", a)
	}
}

func TestClaudePlanFromTypeOrTier(t *testing.T) {
	cases := []struct {
		raw  []string
		want string
	}{
		{[]string{"claude_max", ""}, "max"},
		{[]string{"", "default_claude_max_5x"}, "max"},
		{[]string{"claude_pro", ""}, "pro"},
		{[]string{"claude_team", ""}, "team"},
		{[]string{"claude_enterprise", ""}, "enterprise"},
		{[]string{"", ""}, ""},
	}
	for _, c := range cases {
		if got := claudePlan(c.raw...); got != c.want {
			t.Errorf("claudePlan(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestStatuslineUsageCarriesTheAccountButNoRawIDs(t *testing.T) {
	resetClaudeAccountCache(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeJSON(t, home, fakeClaudeJSON)
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	pct := 42
	acct := sessionClaudeAccount(markerAccount{}).account()
	postStatuslineUsage(context.Background(), producer.NewClient(srv.URL, "tok", time.Second), statuslineUsageSnapshot{fiveHourPct: &pct, fiveHourResetAt: 1782049500, account: acct})
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("posts = %d, want 1", len(bodies))
	}
	var got producer.UsageRequest
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.AccountKey != producer.AccountKey("claude", "11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000") || got.Plan != "max" {
		t.Fatalf("usage = %+v, want the account key and plan max", got)
	}
	for _, raw := range []string{"11111111", "66666666", "someone", "example.com"} {
		if strings.Contains(bodies[0], raw) || strings.Contains(logs.String(), raw) {
			t.Fatalf("%q leaked: body %s logs %s", raw, bodies[0], logs.String())
		}
	}
}

const fakeWorkClaudeJSON = `{"oauthAccount":{"accountUuid":"work-acct","organizationUuid":"work-org","organizationType":"claude_team"}}`

var (
	personalKey = producer.AccountKey("claude", "11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000")
	workKey     = producer.AccountKey("claude", "work-acct", "work-org")
)

func enrichAs(t *testing.T, h *usageRelayHarness, configDir, session string, fiveHour int) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	defer t.Setenv("CLAUDE_CONFIG_DIR", "")
	week := 10
	if err := enrichMarker(h.sessionsDir(), session, &fiveHour, nil, nil, "", &week, nil, "", sessionClaudeAccount); err != nil {
		t.Fatal(err)
	}
}

type relayedUsage struct {
	AccountKey string                     `json:"account_key"`
	Plan       string                     `json:"plan"`
	FiveHour   *producerWindow            `json:"five_hour"`
	Models     map[string]*producerWindow `json:"models"`
}

func relayed(t *testing.T, h *usageRelayHarness) map[string]relayedUsage {
	t.Helper()
	out := map[string]relayedUsage{}
	for _, b := range h.usageBodies {
		var u relayedUsage
		if err := json.Unmarshal([]byte(b), &u); err != nil {
			t.Fatal(err)
		}
		if _, dup := out[u.AccountKey]; dup {
			t.Fatalf("account %q posted twice in one tick: %v", u.AccountKey, h.usageBodies)
		}
		out[u.AccountKey] = u
	}
	return out
}

func TestStatuslineRelayPostsEachSessionUnderItsOwnConfigDirsAccount(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	t.Cleanup(usageModels.reset)
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	work := filepath.Join(h.home, ".claude-work")
	writeClaudeJSON(t, work, fakeWorkClaudeJSON)
	writeMarkerFile(t, h.sessionsDir(), "personal", "")
	writeMarkerFile(t, h.sessionsDir(), "work", "")
	enrichAs(t, h, "", "personal", 20)
	enrichAs(t, h, work, "work", 80)
	usageModels.set(personalKey, map[string]*producer.UsageWindow{"opus": {UsedPercent: 33}})
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if len(got) != 2 || got[personalKey].FiveHour.UsedPercent != 20 || got[personalKey].Plan != "max" ||
		got[workKey].FiveHour.UsedPercent != 80 || got[workKey].Plan != "team" {
		t.Fatalf("relayed = %+v, want each session under its own account", got)
	}
	if got[personalKey].Models["opus"] == nil || got[workKey].Models != nil {
		t.Fatalf("models = %+v / %+v, want the endpoint's models only on its own account", got[personalKey].Models, got[workKey].Models)
	}
}

func TestStatuslineRelayKeepsAnOldSessionOnItsAccountAfterALoginSwitch(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	writeMarkerFile(t, h.sessionsDir(), "before", "")
	writeMarkerFile(t, h.sessionsDir(), "after", "")
	enrichAs(t, h, "", "before", 20)
	writeClaudeJSON(t, h.home, fakeWorkClaudeJSON)
	enrichAs(t, h, "", "after", 80)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if len(got) != 2 || got[personalKey].FiveHour.UsedPercent != 20 || got[workKey].FiveHour.UsedPercent != 80 {
		t.Fatalf("relayed = %+v, want the pre-switch snapshot under the old account only", got)
	}
	h.usageBodies = nil
	enrichAs(t, h, "", "before", 21)
	dispatchTick(context.Background(), cfg)
	if got := relayed(t, h); got[personalKey].FiveHour != nil && got[personalKey].FiveHour.UsedPercent == 21 {
		t.Fatalf("relayed = %+v, want a session's render after /login under the new account", got)
	}
}

func TestStatuslineMarkerWithoutAnAccountIsPostedKeyless(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	writeMarkerFile(t, h.sessionsDir(), "legacy", `,"rate_window_pct":40`)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if u, ok := got[""]; len(got) != 1 || !ok || u.Plan != "" {
		t.Fatalf("relayed = %+v, want one keyless post, never the daemon's account", got)
	}
}

func TestEndpointPollDropsAResponseWhenTheAccountSwitchesDuringTheFetch(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	t.Cleanup(usageModels.reset)
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	cfg, _ := loadConfig()
	client := NewDaemonClient(cfg)
	resp := usageResponse{FiveHour: usageWindow{Utilization: 55}}
	pollUsage(context.Background(), client, func(context.Context) (usageResponse, int, error) {
		writeClaudeJSON(t, h.home, fakeWorkClaudeJSON)
		return resp, http.StatusOK, nil
	})
	if len(h.usageBodies) != 0 || usageModels.get(workKey) != nil || usageModels.get(personalKey) != nil {
		t.Fatalf("posts = %v, want the response fetched across a switch dropped", h.usageBodies)
	}
	pollUsage(context.Background(), client, func(context.Context) (usageResponse, int, error) {
		return resp, http.StatusOK, nil
	})
	if got := relayed(t, h); len(got) != 1 || got[workKey].FiveHour.UsedPercent != 55 || usageModels.get(workKey) == nil {
		t.Fatalf("relayed = %+v, want the next poll under the new account", got)
	}
}

type markerCheckingWriter struct {
	t       *testing.T
	marker  string
	written bool
	keyed   bool
}

func (w *markerCheckingWriter) Write(p []byte) (int, error) {
	raw, _ := os.ReadFile(w.marker)
	w.written, w.keyed = true, strings.Contains(string(raw), "usage_account_key")
	return len(p), nil
}

func TestStatuslinePrintsBeforeItReadsTheAccount(t *testing.T) {
	resetClaudeAccountCache(t)
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	if err := os.WriteFile(wrappedStatuslinePath(h.home), []byte(`"printf hi"`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := writeMarkerFile(t, h.sessionsDir(), "s1", "")
	out := &markerCheckingWriter{t: t, marker: marker}
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percent":20,"resets_at":1782049500}}}`), out)
	if !out.written || out.keyed {
		t.Fatalf("written %v, marker keyed before the output %v; want the output first", out.written, out.keyed)
	}
	raw, _ := os.ReadFile(marker)
	if !strings.Contains(string(raw), personalKey) {
		t.Fatalf("marker = %s, want the account written after the output", raw)
	}
}

func TestStatuslineReparsesTheConfigOnlyWhenItsMtimeOrSizeMoves(t *testing.T) {
	resetClaudeAccountCache(t)
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	cfgPath := filepath.Join(h.home, ".claude.json")
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	marker := writeMarkerFile(t, h.sessionsDir(), "s1", "")
	in := []byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percent":20,"resets_at":1782049500}}}`)
	statuslineMain(in, io.Discard)
	garbage := strings.Repeat(" ", int(fi.Size()))
	if err := os.WriteFile(cfgPath, []byte(garbage), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cfgPath, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percent":21,"resets_at":1782049500}}}`), io.Discard)
	if raw, _ := os.ReadFile(marker); !strings.Contains(string(raw), personalKey) {
		t.Fatalf("marker = %s, want the cached account kept while mtime and size hold", raw)
	}
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(cfgPath, later, later); err != nil {
		t.Fatal(err)
	}
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percent":22,"resets_at":1782049500}}}`), io.Discard)
	if raw, _ := os.ReadFile(marker); strings.Contains(string(raw), personalKey) {
		t.Fatalf("marker = %s, want a re-parse once the mtime moved", raw)
	}
}

func TestStatuslineRelayDropsPreUpgradeKeylessMarkersOnceTheConfigDirIsKeyed(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	writeMarkerFile(t, h.sessionsDir(), "legacy", `,"rate_window_pct":40`)
	writeMarkerFile(t, h.sessionsDir(), "keyed", "")
	enrichAs(t, h, "", "keyed", 20)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if _, keyless := got[""]; keyless || len(got) != 1 || got[personalKey].FiveHour.UsedPercent != 20 {
		t.Fatalf("relayed = %+v, want only the keyed account", got)
	}
}

func TestStatuslineRelaySkipsAMarkerWhoseSnapshotIsOld(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	old := time.Now().Add(-statuslineUsageMaxAge - time.Minute).Unix()
	writeMarkerFile(t, h.sessionsDir(), "dead", fmt.Sprintf(`,"rate_window_pct":40,"statusline_at":%d,"usage_account_key":"%s","usage_account_sig":"x|1|2"`, old, workKey))
	writeMarkerFile(t, h.sessionsDir(), "live", fmt.Sprintf(`,"rate_window_pct":20,"statusline_at":%d,"usage_account_key":"%s","usage_account_sig":"x|1|2"`, time.Now().Unix(), personalKey))
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if _, ok := got[workKey]; ok || got[personalKey].FiveHour == nil {
		t.Fatalf("relayed = %+v, want the old login's stale snapshot left out", got)
	}
}
