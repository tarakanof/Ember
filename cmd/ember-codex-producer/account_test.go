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
	setAuthTime(t, home, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
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

func TestWatcherUsageCarriesTheRolloutWindowLengths(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	reset := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	tok := `{"timestamp":"2026-10-05T08:00:10Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":30,"window_minutes":240,"resets_at":` + reset + `},"secondary":{"used_percent":5,"window_minutes":10080,"resets_at":` + reset + `}}}}`
	writeRollout(t, dir, "rollout-a.jsonl", now, metaCLI, evStarted, tok)
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].FiveHour == nil || usages[0].FiveHour.WindowMinutes != 240 || usages[0].SevenDay.WindowMinutes != 10080 {
		t.Fatalf("usages = %+v, want the rollout's window lengths", usages)
	}
}

func TestAppServerRateLimitsCarryTheWindowLengths(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	reset := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":55,"windowDurationMins":300,"resetsAt":`+reset+`}}}`))
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].FiveHour == nil || usages[0].FiveHour.WindowMinutes != 300 {
		t.Fatalf("usages = %+v, want window_minutes 300", usages)
	}
}

func setAuthTime(t *testing.T, home string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(home, "auth.json"), at, at); err != nil {
		t.Fatal(err)
	}
}

func writeHistory(t *testing.T, dir string, doc accountHistoryDoc) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, accountHistoryFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func metaAt(id string, at time.Time) string {
	return `{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"id":"` + id + `","source":"cli","originator":"codex-tui"}}`
}

func rateLine(at time.Time, pct int, reset time.Time) string {
	return `{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":` + strconv.Itoa(pct) + `,"resets_at":` + strconv.FormatInt(reset.Unix(), 10) + `}}}}`
}

func switchWatcher(t *testing.T, dir, home string, now *time.Time) *watcher {
	t.Helper()
	w := newTestWatcher(dir, *now)
	w.now = func() time.Time { return *now }
	w.cfg.RatePctEnabled = true
	w.cfg.CodexHome = home
	return w
}

func TestWatcherUsageNeverPostsTheOldAccountsRatesUnderTheNewOne(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	setAuthTime(t, home, t0.Add(-time.Hour))
	metaA := metaAt("u-a", t0.Add(-30*time.Minute))
	first := rateLine(t0.Add(-time.Minute), 30, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, first)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-a") {
		t.Fatalf("usages = %+v, want A's", usages)
	}
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	setAuthTime(t, home, t0)
	now = t0.Add(10 * time.Second)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages after the switch = %+v, want A's cached rates dropped", usages)
	}
	writeRollout(t, dir, "rollout-a.jsonl", now, metaA, evStarted, first, rateLine(t0.Add(5*time.Second), 7, reset))
	now = t0.Add(20 * time.Second)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages = %+v, want nothing from a session still running on the old login", usages)
	}
	writeRollout(t, dir, "rollout-b.jsonl", now, metaAt("u-b", t0.Add(15*time.Second)), evStarted, rateLine(t0.Add(18*time.Second), 9, reset))
	now = t0.Add(30 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 9 {
		t.Fatalf("usages = %+v, want B's own rates from the session started after the switch", usages)
	}
}

func TestWatcherUsageSkipsOldLoginRatesAfterASwitchWhileTheDaemonWasDown(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeHistory(t, dir, accountHistoryDoc{Eras: []accountEra{{Key: producer.AccountKey("codex", "acct-a")}}, CheckedAt: t0.Add(-time.Hour)})
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	setAuthTime(t, home, t0.Add(-10*time.Second))
	metaA := metaAt("u-a", t0.Add(-30*time.Minute))
	first := rateLine(t0.Add(-time.Minute), 30, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, first)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("first tick usages = %+v, want the pre-switch rates skipped", usages)
	}
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(5*time.Second), metaA, evStarted, first, rateLine(t0.Add(5*time.Second), 31, reset))
	now = t0.Add(6 * time.Second)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages = %+v, want nothing from a session that started before the switch", usages)
	}
	writeRollout(t, dir, "rollout-b.jsonl", now, metaAt("u-b", t0.Add(2*time.Second)), evStarted, rateLine(t0.Add(4*time.Second), 9, reset))
	now = t0.Add(8 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 9 {
		t.Fatalf("usages = %+v, want B's rates from the new session", usages)
	}
}

func TestWatcherUsageSurvivesATokenRefresh(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	setAuthTime(t, home, t0.Add(-time.Hour))
	metaA := metaAt("u-a", t0.Add(-30*time.Minute))
	first := rateLine(t0.Add(-time.Minute), 30, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, first)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 {
		t.Fatalf("usages = %+v, want A's", usages)
	}
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus", "refreshed": true})
	setAuthTime(t, home, t0.Add(2*time.Second))
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(3*time.Second), metaA, evStarted, first, rateLine(t0.Add(3*time.Second), 33, reset))
	now = t0.Add(4 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-a") || usages[0].FiveHour.UsedPercent != 33 {
		t.Fatalf("usages = %+v, want A's new rates after a token refresh", usages)
	}
}

func TestAppServerUsageCarriesNoOldLoginWindowAfterASwitch(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	setAuthTime(t, home, t0.Add(-time.Hour))
	now := t0
	w := switchWatcher(t, dir, home, &now)
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	as.now = func() time.Time { return now }
	reset := strconv.FormatInt(t0.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":50,"resetsAt":`+reset+`},"secondary":{"usedPercent":60,"resetsAt":`+reset+`}}}`))
	if _, _, usages := cycle(w, as); len(usages) != 1 || usages[0].SevenDay == nil {
		t.Fatalf("usages = %+v, want A's two windows", usages)
	}
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	setAuthTime(t, home, t0.Add(time.Second))
	now = t0.Add(2 * time.Second)
	if _, _, usages := cycle(w, as); len(usages) != 0 {
		t.Fatalf("usages after the switch = %+v, want none", usages)
	}
	now = t0.Add(3 * time.Second)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":5,"resetsAt":`+reset+`},"secondary":null}}`))
	now = t0.Add(4 * time.Second)
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 5 || usages[0].SevenDay != nil {
		t.Fatalf("usages = %+v, want B's 5h window alone, not A's weekly", usages)
	}
}

func TestWatcherUsageFirstRunTrustsExistingSessions(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaAt("u-a", t0.Add(-72*time.Hour)), evStarted, rateLine(t0.Add(-time.Minute), 30, t0.Add(time.Hour)))
	now := t0.Add(5 * time.Second)
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-a") {
		t.Fatalf("usages = %+v, want the old session's rates on the first run", usages)
	}
	b, err := os.ReadFile(filepath.Join(dir, accountHistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, accountHistoryFile))
	if fi.Mode().Perm() != 0o600 || strings.Contains(string(b), "acct-a") || !strings.Contains(string(b), producer.AccountKey("codex", "acct-a")) {
		t.Fatalf("history file %o %s, want 0600 with the hashed key only", fi.Mode().Perm(), b)
	}
}

func TestWatcherUsageSurvivesARestartAfterATokenRefresh(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	keyA := producer.AccountKey("codex", "acct-a")
	writeHistory(t, dir, accountHistoryDoc{Eras: []accountEra{{Key: keyA}}, CheckedAt: t0.Add(-time.Hour)})
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus", "refreshed": true})
	setAuthTime(t, home, t0.Add(-10*time.Minute))
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaAt("u-a", t0.Add(-72*time.Hour)), evStarted, rateLine(t0.Add(-time.Minute), 30, t0.Add(time.Hour)))
	now := t0.Add(5 * time.Second)
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 || usages[0].AccountKey != keyA {
		t.Fatalf("usages = %+v, want A's rates after a restart that followed a refresh", usages)
	}
}

func TestWatcherUsageFromASessionResumedUnderTheNewLogin(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	metaA := metaAt("u-a", t0.Add(-3*time.Hour))
	old := rateLine(t0.Add(-150*time.Minute), 30, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, old)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 {
		t.Fatalf("usages = %+v, want A's", usages)
	}
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	now = t0.Add(10 * time.Second)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages after the switch = %+v, want none", usages)
	}
	resumed := `{"timestamp":"` + t0.Add(15*time.Second).UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_started"}}`
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(20*time.Second), metaA, evStarted, old, resumed, rateLine(t0.Add(20*time.Second), 4, reset))
	now = t0.Add(21 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 4 {
		t.Fatalf("usages = %+v, want B's rates from the resumed session", usages)
	}
}

func TestWatcherUsageTakesTheSwitchFromTheRenameNotTheKeptMtime(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeHistory(t, dir, accountHistoryDoc{Eras: []accountEra{{Key: producer.AccountKey("codex", "acct-a")}}, CheckedAt: t0.Add(-time.Hour)})
	saved := t.TempDir()
	writeCodexAuth(t, saved, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	setAuthTime(t, saved, t0.Add(-2*time.Hour))
	if err := os.Rename(filepath.Join(saved, "auth.json"), filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaAt("u-a", t0.Add(-30*time.Minute)), evStarted, rateLine(t0.Add(-time.Minute), 30, t0.Add(time.Hour)))
	now := t0.Add(5 * time.Second)
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages = %+v, want A's session kept off B after a rename that kept B's old mtime", usages)
	}
}

func TestWatcherIgnoresAPartialAuthWrite(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	now := t0
	w := switchWatcher(t, dir, home, &now)
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	as.now = func() time.Time { return now }
	reset := strconv.FormatInt(t0.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":50,"resetsAt":`+reset+`}}}`))
	if _, _, usages := cycle(w, as); len(usages) != 1 {
		t.Fatalf("usages = %+v, want A's", usages)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"id_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(2 * time.Second)
	cycle(w, as)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus", "refreshed": true})
	now = t0.Add(4 * time.Second)
	cycle(w, as)
	as.mu.Lock()
	kept := as.hasRate
	as.mu.Unlock()
	if !kept || len(w.history.eras) != 1 {
		t.Fatalf("hasRate=%v eras=%d, want the snapshot kept and no new era", kept, len(w.history.eras))
	}
}

func TestAppServerUsageDropsTheOldLoginWindowBeforeSelection(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	now := t0.Add(-5 * time.Second)
	w := switchWatcher(t, dir, home, &now)
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	as.now = func() time.Time { return now }
	reset := strconv.FormatInt(t0.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":50,"resetsAt":`+reset+`},"secondary":{"usedPercent":60,"resetsAt":`+reset+`}}}`))
	if _, _, usages := cycle(w, as); len(usages) != 1 || usages[0].SevenDay == nil {
		t.Fatalf("usages = %+v, want A's two windows", usages)
	}
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	now = time.Now().Add(2 * time.Second)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":5,"resetsAt":`+reset+`},"secondary":null}}`))
	now = now.Add(time.Second)
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 5 || usages[0].SevenDay != nil {
		t.Fatalf("usages = %+v, want B's first snapshot alone, without A's weekly", usages)
	}
}

func TestAccountHistory(t *testing.T) {
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	type step struct {
		key      string
		boundary time.Time
		now      time.Time
	}
	type probe struct {
		at    time.Time
		key   string
		known bool
	}
	cases := []struct {
		name   string
		stored *accountHistoryDoc
		steps  []step
		eras   int
		probes []probe
	}{
		{"A then B then A", nil, []step{{"A", at(0), at(1)}, {"B", at(10), at(11)}, {"A", at(20), at(21)}}, 3,
			[]probe{{at(5), "A", true}, {at(10), "B", true}, {at(15), "B", true}, {at(25), "A", true}}},
		{"an exactly equal timestamp is the new era", nil, []step{{"A", at(0), at(1)}, {"B", at(10), at(11)}}, 2,
			[]probe{{at(10).Add(-time.Nanosecond), "A", true}, {at(10), "B", true}}},
		{"trim to 8 eras", nil, []step{{"k0", at(0), at(1)}, {"k1", at(10), at(11)}, {"k2", at(20), at(21)}, {"k3", at(30), at(31)}, {"k4", at(40), at(41)}, {"k5", at(50), at(51)}, {"k6", at(60), at(61)}, {"k7", at(70), at(71)}, {"k8", at(80), at(81)}, {"k9", at(90), at(91)}}, 8,
			[]probe{{at(15), "", false}, {at(25), "k2", true}, {at(95), "k9", true}}},
		{"a partial read is no switch", nil, []step{{"A", at(0), at(1)}, {"", at(5), at(6)}, {"A", at(7), at(8)}}, 1,
			[]probe{{at(9), "A", true}}},
		{"a stale boundary falls back to the last check", nil, []step{{"A", at(0), at(1)}, {"B", at(-30), at(3)}}, 2,
			[]probe{{at(1).Add(-time.Second), "A", true}, {at(1), "B", true}}},
		{"a future boundary is clamped to now", nil, []step{{"A", at(0), at(1)}, {"B", at(60), at(3)}}, 2,
			[]probe{{at(2), "A", true}, {at(3), "B", true}}},
		{"a clock rollback takes the boundary", nil, []step{{"A", at(0), at(30)}, {"B", at(5), at(6)}}, 2,
			[]probe{{at(4), "A", true}, {at(5), "B", true}}},
		{"first run trusts everything", nil, []step{{"A", at(0), at(1)}}, 1,
			[]probe{{at(-10000), "A", true}, {time.Time{}, "", false}}},
		{"a restart after a refresh keeps the eras", &accountHistoryDoc{Eras: []accountEra{{Key: "A"}}, CheckedAt: at(-60)}, []step{{"A", at(-10), at(1)}}, 1,
			[]probe{{at(-5000), "A", true}}},
		{"a switch while down starts at the later of the last check and the boundary", &accountHistoryDoc{Eras: []accountEra{{Key: "A"}}, CheckedAt: at(-60)}, []step{{"B", at(-120), at(1)}}, 2,
			[]probe{{at(-61), "A", true}, {at(-60), "B", true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), accountHistoryFile)
			if tc.stored != nil {
				b, _ := json.Marshal(tc.stored)
				if err := os.WriteFile(path, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := accountHistory{path: path}
			for _, s := range tc.steps {
				h.observe(s.key, s.boundary, true, s.now)
			}
			if len(h.eras) != tc.eras {
				t.Fatalf("eras = %+v, want %d", h.eras, tc.eras)
			}
			for _, p := range tc.probes {
				if key, known := h.at(p.at); key != p.key || known != p.known {
					t.Errorf("at(%v) = %q %v, want %q %v", p.at, key, known, p.key, p.known)
				}
			}
			reloaded := accountHistory{path: path}
			reloaded.load()
			if len(reloaded.eras) != len(h.eras) {
				t.Errorf("reloaded %d eras, want %d", len(reloaded.eras), len(h.eras))
			}
		})
	}
}

func TestFold_ALongGapStartsASegment(t *testing.T) {
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	ev := func(at time.Time) string {
		return `{"timestamp":"` + at.Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_started"}}`
	}
	d := foldAll([]string{ev(t0), rateLine(t0.Add(time.Minute), 1, t0.Add(time.Hour)), ev(t0.Add(29 * time.Minute))}, false)
	if !d.segmentAt.IsZero() || !d.rateStart.IsZero() {
		t.Fatalf("segmentAt %v rateStart %v, want none under 30 min", d.segmentAt, d.rateStart)
	}
	d = foldAll([]string{ev(t0), ev(t0.Add(30 * time.Minute)), rateLine(t0.Add(31*time.Minute), 1, t0.Add(time.Hour))}, false)
	if !d.rateStart.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("rateStart = %v, want the line after the gap", d.rateStart)
	}
}

func TestWatcherUsageFromAQuickResumeUnderTheNewLogin(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	metaA := metaAt("u-a", t0.Add(-3*time.Hour))
	old := rateLine(t0.Add(-2*time.Minute), 100, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, old)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	w.tick()
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	now = t0.Add(10 * time.Second)
	w.tick()
	resumed := `{"timestamp":"` + t0.Add(60*time.Second).UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_started"}}`
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(70*time.Second), metaA, evStarted, old, resumed, rateLine(t0.Add(70*time.Second), 4, t0.Add(5*time.Hour)))
	now = t0.Add(71 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].FiveHour.UsedPercent != 4 {
		t.Fatalf("usages = %+v, want B's rates from a resume a minute after the switch", usages)
	}
}

func TestWatcherUsageKeepsAnOldLoginTurnRunningAcrossTheSwitchOffTheNewKey(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	metaA := metaAt("u-a", t0.Add(-time.Hour))
	old := rateLine(t0.Add(-2*time.Second), 40, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, old)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	w.tick()
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	now = t0.Add(3 * time.Second)
	w.tick()
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(8*time.Second), metaA, evStarted, old, rateLine(t0.Add(8*time.Second), 41, reset))
	now = t0.Add(9 * time.Second)
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("usages = %+v, want nothing from a turn still running on the old login", usages)
	}
}

func TestCycleDropsTheOldLoginWindowWhenTheSwitchLandsMidCycle(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	now := t0.Add(-5 * time.Second)
	w := switchWatcher(t, dir, home, &now)
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	as.now = func() time.Time { return now }
	reset := strconv.FormatInt(t0.Add(time.Hour).Unix(), 10)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":50,"resetsAt":`+reset+`},"secondary":{"usedPercent":60,"resetsAt":`+reset+`}}}`))
	cycle(w, as)
	now = time.Now().Add(2 * time.Second)
	acct, _, switched := w.observeAccount(now)
	if switched {
		t.Fatal("switch seen before auth.json changed")
	}
	ap := as.tick()
	writeCodexAuth(t, home, "acct-b", map[string]any{"chatgpt_plan_type": "pro"})
	w.owned, w.pending, w.rateExtra = ap.owned, ap.pending, ap.rate
	w.tickWith(acct)
	now = now.Add(time.Second)
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":5,"resetsAt":`+reset+`},"secondary":null}}`))
	now = now.Add(time.Second)
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-b") || usages[0].SevenDay != nil {
		t.Fatalf("usages = %+v, want B's 5h window alone", usages)
	}
}

func TestAccountHistoryCorruptFileIsAFirstRun(t *testing.T) {
	for _, body := range []string{"", "{", `{"eras":null}`, `{"eras":[{"key":"x","since":"bogus"}]}`, `[]`, "\x00\x01"} {
		path := filepath.Join(t.TempDir(), accountHistoryFile)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		h := accountHistory{path: path}
		now := time.Now()
		h.observe("A", now.Add(-time.Hour), true, now)
		if k, ok := h.at(now.Add(-1000 * time.Hour)); !ok || k != "A" {
			t.Errorf("body %q: at(old) = %q %v, want first-run trust", body, k, ok)
		}
		b, _ := os.ReadFile(path)
		var doc accountHistoryDoc
		if json.Unmarshal(b, &doc) != nil || len(doc.Eras) != 1 || doc.Eras[0].Key != "A" {
			t.Errorf("body %q: not rewritten: %q", body, b)
		}
	}
}

type fakeFileInfo struct{ mod time.Time }

func (f fakeFileInfo) Name() string       { return "auth.json" }
func (f fakeFileInfo) Size() int64        { return 1 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o600 }
func (f fakeFileInfo) ModTime() time.Time { return f.mod }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestAuthBoundaryTakesTheSymlinksOwnMtime(t *testing.T) {
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	prevStat, prevLstat := statFile, lstatFile
	t.Cleanup(func() { statFile, lstatFile = prevStat, prevLstat })
	statFile = func(string) (os.FileInfo, error) { return fakeFileInfo{t0.Add(-2 * time.Hour)}, nil }
	lstatFile = func(string) (os.FileInfo, error) { return fakeFileInfo{t0}, nil }
	if at, ok := authBoundary("/codex-home"); !ok || !at.Equal(t0) {
		t.Fatalf("boundary = %v %v, want the link's own mtime %v", at, ok, t0)
	}
}

func TestWatcherUsageKeepsTheKeyThroughAPartialAuthWrite(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	reset := t0.Add(time.Hour)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	metaA := metaAt("u-a", t0.Add(-time.Hour))
	first := rateLine(t0.Add(-time.Minute), 30, reset)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaA, evStarted, first)
	now := t0
	w := switchWatcher(t, dir, home, &now)
	if _, _, usages := w.tick(); len(usages) != 1 {
		t.Fatalf("usages = %+v, want A's", usages)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"id_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, dir, "rollout-a.jsonl", t0.Add(2*time.Second), metaA, evStarted, first, rateLine(t0.Add(2*time.Second), 31, reset))
	now = t0.Add(3 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-a") || usages[0].FiveHour.UsedPercent != 31 {
		t.Fatalf("usages = %+v, want A's new rates under A's key during a partial write", usages)
	}
}

func TestWatcherFirstTickWithoutAKeyIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeRollout(t, dir, "rollout-a.jsonl", t0, metaAt("u-a", t0.Add(-72*time.Hour)), evStarted, rateLine(t0.Add(-time.Minute), 30, t0.Add(time.Hour)))
	now := t0
	w := switchWatcher(t, dir, home, &now)
	w.tick()
	if _, err := os.Stat(filepath.Join(dir, accountHistoryFile)); !os.IsNotExist(err) {
		t.Fatalf("history saved for a keyless first tick: %v", err)
	}
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	now = t0.Add(2 * time.Second)
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].AccountKey != producer.AccountKey("codex", "acct-a") {
		t.Fatalf("usages = %+v, want the existing session's rates under the first real key", usages)
	}
}

func TestAccountHistorySavesTheCheckTimeEveryTenMinutesAndOnShutdown(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t0 := time.Now().Truncate(time.Second)
	writeCodexAuth(t, home, "acct-a", map[string]any{"chatgpt_plan_type": "plus"})
	now := t0
	w := switchWatcher(t, dir, home, &now)
	checked := func() time.Time {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, accountHistoryFile))
		if err != nil {
			t.Fatal(err)
		}
		var doc accountHistoryDoc
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		return doc.CheckedAt
	}
	w.tick()
	now = t0.Add(9 * time.Minute)
	w.tick()
	if got := checked(); !got.Equal(t0) {
		t.Fatalf("checked_at = %v after 9 min, want %v (no write yet)", got, t0)
	}
	now = t0.Add(10 * time.Minute)
	w.tick()
	if got := checked(); !got.Equal(now) {
		t.Fatalf("checked_at = %v after 10 min, want %v", got, now)
	}
	now = t0.Add(11 * time.Minute)
	w.tick()
	shutdown(w, nil)
	if got := checked(); !got.Equal(now) {
		t.Fatalf("checked_at = %v after shutdown, want %v", got, now)
	}
}
