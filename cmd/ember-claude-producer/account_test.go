package main

import (
	"context"
	"encoding/json"
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
		claudeAccounts.path, claudeAccounts.acct = "", claudeAccount{}
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
	postStatuslineUsage(context.Background(), producer.NewClient(srv.URL, "tok", time.Second), statuslineUsageSnapshot{fiveHourPct: &pct, fiveHourResetAt: 1782049500})
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
