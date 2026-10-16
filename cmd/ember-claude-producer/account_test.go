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
	"syscall"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	if os.Getenv("EMBER_TEST_STATUSLINE_CHILD") == "1" {
		runStatusline()
	}
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

func TestStatuslineUpdatesTheMarkerWhileTheWrappedCommandRuns(t *testing.T) {
	resetClaudeAccountCache(t)
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	flag := filepath.Join(h.home, "release")
	cmd, _ := json.Marshal(`while [ ! -f '` + flag + `' ]; do sleep 0.02; done; printf hi`)
	if err := os.WriteFile(wrappedStatuslinePath(h.home), cmd, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMBER_STATUSLINE_TIMEOUT_MS", "4000")
	marker := writeMarkerFile(t, h.sessionsDir(), "s1", "")
	released := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if raw, _ := os.ReadFile(marker); strings.Contains(string(raw), personalKey) && strings.Contains(string(raw), `"rate_window_pct":20`) {
				_ = os.WriteFile(flag, nil, 0o600)
				released <- true
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = os.WriteFile(flag, nil, 0o600)
		released <- false
	}()
	var out strings.Builder
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":20,"resets_at":1782049500}}}`), &out)
	if !<-released {
		raw, _ := os.ReadFile(marker)
		t.Fatalf("the marker waited for the wrapped command: %s", raw)
	}
	if out.String() != "hi" {
		t.Fatalf("output = %q, want the wrapped command's", out.String())
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
	in := []byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":20,"resets_at":1782049500}}}`)
	statuslineMain(in, io.Discard)
	garbage := strings.Repeat(" ", int(fi.Size()))
	if err := os.WriteFile(cfgPath, []byte(garbage), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cfgPath, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":21,"resets_at":1782049500}}}`), io.Discard)
	if raw, _ := os.ReadFile(marker); !strings.Contains(string(raw), personalKey) {
		t.Fatalf("marker = %s, want the cached account kept while mtime and size hold", raw)
	}
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(cfgPath, later, later); err != nil {
		t.Fatal(err)
	}
	statuslineMain([]byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":22,"resets_at":1782049500}}}`), io.Discard)
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

func TestStatuslineRelayKeepsAKeylessSessionWhoseConfigIsMissing(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeClaudeJSON(t, h.home, fakeClaudeJSON)
	empty := filepath.Join(h.home, ".claude-empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMarkerFile(t, h.sessionsDir(), "nocfg", "")
	writeMarkerFile(t, h.sessionsDir(), "keyed", "")
	enrichAs(t, h, empty, "nocfg", 40)
	enrichAs(t, h, "", "keyed", 20)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := relayed(t, h)
	if len(got) != 2 || got[""].FiveHour == nil || got[""].FiveHour.UsedPercent != 40 || got[personalKey].FiveHour.UsedPercent != 20 {
		t.Fatalf("relayed = %+v, want the keyless session kept beside the keyed one", got)
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

type configFIFO struct {
	opened chan struct{}
	bodies chan string
}

func makeConfigFIFO(t *testing.T, dir string) *configFIFO {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".claude.json")
	_ = os.Remove(p)
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	c := &configFIFO{opened: make(chan struct{}), bodies: make(chan string)}
	stop := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for {
			f, err := os.OpenFile(p, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			select {
			case c.opened <- struct{}{}:
			case <-stop:
				_ = f.Close()
				return
			}
			select {
			case body := <-c.bodies:
				_ = os.Remove(p)
				_ = syscall.Mkfifo(p, 0o600)
				_, _ = f.WriteString(body)
				_ = f.Close()
			case <-stop:
				_ = f.Close()
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		if f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		<-exited
	})
	return c
}

const fifoWait = 5 * time.Second

func (c *configFIFO) waitReader(t *testing.T, what string) {
	t.Helper()
	select {
	case <-c.opened:
	case <-time.After(fifoWait):
		t.Fatalf("nobody read the config: %s", what)
	}
}

func (c *configFIFO) answer(t *testing.T, body string) {
	t.Helper()
	select {
	case c.bodies <- body:
	case <-time.After(fifoWait):
		t.Fatal("the config reader went away before the answer")
	}
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(fifoWait):
		t.Fatalf("%s did not return; it is blocked on another read of the config", what)
	}
}

func renderAsync(in statuslineInput) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		enrichFromStatusline(in, nil)
		close(done)
	}()
	return done
}

func statuslineWithFiveHour(t *testing.T, session string, pct int) statuslineInput {
	t.Helper()
	in, ok := parseStatusline([]byte(fmt.Sprintf(`{"session_id":%q,"rate_limits":{"five_hour":{"used_percentage":%d}}}`, session, pct)))
	if !ok {
		t.Fatal("parse failed")
	}
	return in
}

func markerAt(t *testing.T, path string) marker {
	t.Helper()
	var m marker
	raw, err := readMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDispatchTickNeverReadsTheClaudeConfig(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	fifo := makeConfigFIFO(t, h.home)
	writeMarkerFile(t, h.sessionsDir(), "s", fmt.Sprintf(`,"rate_window_pct":40,"statusline_at":%d,"usage_account_key":%q`, time.Now().Unix(), personalKey))
	cfg, _ := loadConfig()
	done := make(chan struct{})
	go func() {
		dispatchTick(context.Background(), cfg)
		close(done)
	}()
	select {
	case <-done:
	case <-fifo.opened:
		fifo.answer(t, fakeClaudeJSON)
		waitDone(t, done, "dispatchTick")
		t.Fatal("dispatchTick read the config; the tick must take the account from the marker")
	case <-time.After(fifoWait):
		t.Fatal("dispatchTick did not return")
	}
	if got := relayed(t, h); len(got) != 1 || got[personalKey].FiveHour == nil {
		t.Fatalf("relayed = %+v, want the marker's account", got)
	}
}

func TestClaudeAccountCacheKeepsTheParseWhileMtimeAndSizeHold(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeJSON(t, home, fakeWorkClaudeJSON)
	if a := claudeAccounts.current(); a.key != workKey {
		t.Fatalf("key = %q, want the work account", a.key)
	}
	p := filepath.Join(home, ".claude.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeJSON(t, home, strings.Replace(fakeWorkClaudeJSON, "work-acct", "work-acdt", 1))
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if a := claudeAccounts.current(); a.key != workKey {
		t.Fatalf("key = %q, want the cached parse while mtime and size are unchanged", a.key)
	}
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if a := claudeAccounts.current(); a.key != producer.AccountKey("claude", "work-acdt", "work-org") {
		t.Fatalf("key = %q, want a re-parse once the mtime moved", a.key)
	}
}

func TestStatuslineWritesTheMarkerBeforeParsingTheAccount(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	mp := writeMarkerFile(t, filepath.Join(home, ".local", "state", "ember", "sessions"), "s", "")
	cfgDir := filepath.Join(home, ".claude-work")
	fifo := makeConfigFIFO(t, cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	done := renderAsync(statuslineWithFiveHour(t, "s", 55))
	fifo.waitReader(t, "the render")
	if m := markerAt(t, mp); m.RateWindowPct == nil || *m.RateWindowPct != 55 || m.UsageAccountKey != "" || !m.UsageAccountPending {
		t.Fatalf("marker during the parse = pct %v key %q pending %v, want the figures written and the account pending",
			m.RateWindowPct, m.UsageAccountKey, m.UsageAccountPending)
	}
	fifo.answer(t, fakeWorkClaudeJSON)
	waitDone(t, done, "the render")
	if m := markerAt(t, mp); m.UsageAccountKey != workKey || m.UsagePlan != "team" || m.UsageAccountSig == "" || m.UsageAccountPending {
		t.Fatalf("marker account = %q plan %q pending %v, want the parsed account and no pending flag", m.UsageAccountKey, m.UsagePlan, m.UsageAccountPending)
	}
}

func TestStatuslineAccountRewriteKeepsNewerFigures(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessions := filepath.Join(home, ".local", "state", "ember", "sessions")
	mp := writeMarkerFile(t, sessions, "s", "")
	cfgDir := filepath.Join(home, ".claude-work")
	fifo := makeConfigFIFO(t, cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	done := renderAsync(statuslineWithFiveHour(t, "s", 55))
	fifo.waitReader(t, "the render")
	if err := enrichMarker(sessions, "s", ratePtr(56), nil, nil, "", nil, nil, "", keepAccount); err != nil {
		t.Fatal(err)
	}
	fifo.answer(t, fakeWorkClaudeJSON)
	waitDone(t, done, "the render")
	if m := markerAt(t, mp); m.RateWindowPct == nil || *m.RateWindowPct != 56 || m.UsageAccountKey != workKey {
		t.Fatalf("marker = pct %v key %q, want the concurrent render's 56 under the parsed account", m.RateWindowPct, m.UsageAccountKey)
	}
}

func TestStatuslineRelayHoldsFiguresBackWhileTheirAccountIsParsed(t *testing.T) {
	resetClaudeAccountCache(t)
	usageModels.reset()
	h := newUsageRelayHarness(t)
	cfgDir := filepath.Join(h.home, ".claude-alt")
	writeClaudeJSON(t, cfgDir, fakeClaudeJSON)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	writeMarkerFile(t, h.sessionsDir(), "s", "")
	enrichFromStatusline(statuslineWithFiveHour(t, "s", 20), nil)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if got := relayed(t, h); got[personalKey].FiveHour == nil || got[personalKey].FiveHour.UsedPercent != 20 {
		t.Fatalf("relayed = %+v, want 20 under the personal account", got)
	}
	fifo := makeConfigFIFO(t, cfgDir)
	done := renderAsync(statuslineWithFiveHour(t, "s", 80))
	fifo.waitReader(t, "the render after /login")
	h.usageBodies = nil
	dispatchTick(context.Background(), cfg)
	for key, u := range relayed(t, h) {
		if u.FiveHour != nil && u.FiveHour.UsedPercent == 80 {
			t.Fatalf("relayed 80 under %q while the new account was still being parsed", key)
		}
	}
	fifo.answer(t, fakeWorkClaudeJSON)
	waitDone(t, done, "the render")
	h.usageBodies = nil
	dispatchTick(context.Background(), cfg)
	if got := relayed(t, h); got[workKey].FiveHour == nil || got[workKey].FiveHour.UsedPercent != 80 || got[personalKey].FiveHour != nil {
		t.Fatalf("relayed = %+v, want 80 under the work account only", got)
	}
}

func TestStatuslineSkipsTheAccountRewriteWhenTheConfigIsUnchanged(t *testing.T) {
	resetClaudeAccountCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessions := filepath.Join(home, ".local", "state", "ember", "sessions")
	mp := writeMarkerFile(t, sessions, "s", "")
	writeClaudeJSON(t, home, fakeClaudeJSON)
	render := func(pct int) {
		in, _ := parseStatusline([]byte(fmt.Sprintf(`{"session_id":"s","rate_limits":{"five_hour":{"used_percentage":%d}}}`, pct)))
		enrichFromStatusline(in, nil)
	}
	render(30)
	p := filepath.Join(home, ".claude.json")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	render(31)
	var m marker
	raw, _ := readMarker(mp)
	_ = json.Unmarshal(raw, &m)
	if m.UsageAccountKey != personalKey || *m.RateWindowPct != 31 {
		t.Fatalf("marker = key %q pct %v, want the carried-over account and the new figure", m.UsageAccountKey, m.RateWindowPct)
	}
}
