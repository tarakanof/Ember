package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func fakeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"email": "someone@example.com", "https://api.openai.com/auth": claims})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(body) + ".c2lnbmF0dXJl"
}

func writeCodexAuth(t *testing.T, home, accountID string, claims map[string]any) {
	t.Helper()
	doc := map[string]any{
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token": fakeIDToken(t, claims), "access_token": "fake-access", "refresh_token": "fake-refresh",
			"account_id": accountID,
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexAccountKeyFromFakeAuth(t *testing.T) {
	home := t.TempDir()
	writeCodexAuth(t, home, "acct-personal-1", map[string]any{"chatgpt_plan_type": "pro", "chatgpt_user_id": "user-1"})
	var c codexAccountCache
	a := c.current(home)
	if a.key != producer.AccountKey("codex", "acct-personal-1") || a.plan != "pro" {
		t.Fatalf("account = %+v, want the account id alone on pro", a)
	}
}

func TestCodexWorkspaceAccountKeyAddsTheUser(t *testing.T) {
	home := t.TempDir()
	writeCodexAuth(t, home, "ws-1", map[string]any{"chatgpt_plan_type": "team", "chatgpt_user_id": "user-a"})
	var c codexAccountCache
	a := c.current(home)
	if a.key != producer.AccountKey("codex", "ws-1", "user-a") || a.plan != "team" {
		t.Fatalf("account = %+v, want workspace + user on team", a)
	}
	other := t.TempDir()
	writeCodexAuth(t, other, "ws-1", map[string]any{"chatgpt_plan_type": "team", "chatgpt_user_id": "user-b"})
	if b := c.current(other); b.key == a.key {
		t.Fatal("two users of one workspace share a key")
	}
}

func TestCodexAccountWithoutTokensIsKeyless(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-fake"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var c codexAccountCache
	if a := c.current(home); a != (codexAccount{}) {
		t.Fatalf("account = %+v, want none", a)
	}
	if a := c.current(""); a != (codexAccount{}) {
		t.Fatalf("no CODEX_HOME gave %+v", a)
	}
}

func TestCodexIDTokenGarbageGivesNoClaims(t *testing.T) {
	for _, tok := range []string{"", "a.b", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("[1]")) + ".c"} {
		if c := idTokenClaims(tok); c != (codexAuthClaims{}) {
			t.Errorf("idTokenClaims(%q) = %+v", tok, c)
		}
	}
}

func TestWatcherUsageCarriesTheAccountButNoRawIDs(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeCodexAuth(t, home, "acct-raw-123", map[string]any{"chatgpt_plan_type": "plus", "chatgpt_user_id": "user-raw-456"})
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	now := time.Now()
	reset := now.Add(time.Hour).Unix()
	tok := `{"timestamp":"2026-10-05T08:00:10Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":30,"resets_at":` + strconv.FormatInt(reset, 10) + `}}}}`
	writeRollout(t, dir, "rollout-a.jsonl", now, metaCLI, evStarted, tok)
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	w.cfg.CodexHome = home
	_, _, usages := w.tick()
	if len(usages) != 1 {
		t.Fatalf("usages = %+v, want one", usages)
	}
	u := usages[0]
	if u.AccountKey != producer.AccountKey("codex", "acct-raw-123") || u.Plan != "plus" {
		t.Fatalf("usage = %+v, want the key and plan plus", u)
	}
	body, _ := json.Marshal(u)
	for _, raw := range []string{"acct-raw", "user-raw", "someone", "fake-"} {
		if strings.Contains(string(body), raw) || strings.Contains(logs.String(), raw) {
			t.Fatalf("%q leaked: %s / %s", raw, body, logs.String())
		}
	}
}

func TestWatcherUsagePlanFallsBackToTheRolloutPlanType(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	reset := now.Add(time.Hour).Unix()
	tok := `{"timestamp":"2026-10-05T08:00:10Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","plan_type":"Pro","primary":{"used_percent":30,"resets_at":` + strconv.FormatInt(reset, 10) + `}}}}`
	writeRollout(t, dir, "rollout-a.jsonl", now, metaCLI, evStarted, tok)
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != "" || usages[0].Plan != "pro" {
		t.Fatalf("usages = %+v, want a keyless post on pro", usages)
	}
}

func TestAppServerRateLimitsCarryThePlanType(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	reset := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","planType":"plus","primary":{"usedPercent":55,"resetsAt":`+reset+`}}}`))
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].Plan != "plus" {
		t.Fatalf("usages = %+v, want plan plus", usages)
	}
}
