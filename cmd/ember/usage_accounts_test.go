package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	keyMaxA  = "aaaaaaaaaaaaaaa1"
	keyMaxB  = "bbbbbbbbbbbbbbb2"
	keyPro   = "ccccccccccccccc3"
	keyCodex = "ddddddddddddddd4"
)

func postUsage(t *testing.T, srv *httptest.Server, body string) {
	t.Helper()
	resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("usage post %s = %d %s", body, resp.StatusCode, b)
	}
}

func getAccounts(t *testing.T, srv *httptest.Server, token string) usageAccountsOut {
	t.Helper()
	resp, b := devReq(t, srv, "GET", "/v1/usage/accounts", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accounts = %d %s", resp.StatusCode, b)
	}
	var out usageAccountsOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func accountNames(out usageAccountsOut) string {
	var parts []string
	for _, a := range out.Accounts {
		parts = append(parts, a.ID+"="+a.Name)
	}
	return strings.Join(parts, ",")
}

func TestUsageStoreKeepsTwoAccountsOfOneToolApart(t *testing.T) {
	s := newUsageStore()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.Put("claude", ToolUsage{Account: keyMaxA, FiveHour: &UsageWindow{UsedPercent: 20}, UpdatedAt: now})
	s.Put("claude", ToolUsage{Account: keyMaxB, FiveHour: &UsageWindow{UsedPercent: 70}, UpdatedAt: now.Add(time.Second)})
	st := s.state(nil, now.Add(time.Minute))
	if len(st.Accounts) != 2 {
		t.Fatalf("accounts = %+v, want two", st.Accounts)
	}
	if got := st.Tools["claude"]; got.FiveHourPct != 70 || got.Report.Account != keyMaxB {
		t.Fatalf("clock's claude = %+v, want the highest-5h account", got)
	}
}

func TestUsageStoreMergesTwoMacsOnOneAccountNewestWins(t *testing.T) {
	s := newUsageStore()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.Put("claude", ToolUsage{Account: keyMaxA, Source: "m4", FiveHour: &UsageWindow{UsedPercent: 30}, UpdatedAt: now.Add(time.Minute)})
	s.Put("claude", ToolUsage{Account: keyMaxA, Source: "m5", FiveHour: &UsageWindow{UsedPercent: 10}, UpdatedAt: now})
	st := s.state(nil, now.Add(2*time.Minute))
	if len(st.Accounts) != 1 || st.Accounts[0].Report.Source != "m4" || st.Accounts[0].Report.FiveHour.UsedPercent != 30 {
		t.Fatalf("accounts = %+v, want one entry holding the newer m4 post", st.Accounts)
	}
	s.Put("claude", ToolUsage{Account: keyMaxA, Source: "m5", FiveHour: &UsageWindow{UsedPercent: 35}, UpdatedAt: now.Add(3 * time.Minute)})
	if got := s.state(nil, now.Add(4*time.Minute)).Accounts; len(got) != 1 || got[0].Report.Source != "m5" {
		t.Fatalf("accounts = %+v, want m5's newer post", got)
	}
}

func TestUsageStoreDropsAnAccountSevenDaysAfterItsLastPost(t *testing.T) {
	s := newUsageStore()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.Put("claude", ToolUsage{Account: keyMaxA, UpdatedAt: now})
	if got := s.state(nil, now.Add(usageAccountTTL)).Accounts; len(got) != 1 || got[0].Fresh {
		t.Fatalf("at 7 days = %+v, want one stale account", got)
	}
	if got := s.state(nil, now.Add(usageAccountTTL+time.Second)).Accounts; len(got) != 0 {
		t.Fatalf("past 7 days = %+v, want none", got)
	}
	s.Put("codex", ToolUsage{UpdatedAt: now.Add(usageAccountTTL + time.Hour)})
	if _, ok := s.All()[usageKey{"claude", keyMaxA}]; ok {
		t.Fatal("a post past the TTL did not prune the expired account")
	}
}

func TestLegacyKeylessUsagePostIsOneAccountPerTool(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","five_hour":{"used_percent":40}}`)
	postUsage(t, srv, `{"tool":"claude","source":"statusline","five_hour":{"used_percent":45}}`)
	out := getAccounts(t, srv, testToken)
	if accountNames(out) != "claude=Claude" || out.Accounts[0].Plan != nil || out.Accounts[0].Stale {
		t.Fatalf("accounts = %+v, want one fresh keyless Claude", out)
	}
	if u, ok := app.usage.Get("claude"); !ok || u.FiveHour.UsedPercent != 45 {
		t.Fatalf("claude usage = %+v", u)
	}
}

func TestUsagePostRejectsABadAccountKeyOrPlan(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	for _, body := range []string{
		`{"tool":"claude","account_key":"someone@example.com"}`,
		`{"tool":"claude","account_key":"ABCDEFABCDEFABCD"}`,
		`{"tool":"claude","account_key":"abc"}`,
		`{"tool":"claude","plan":"Max Plan"}`,
	} {
		if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
}

func TestUsageAccountDefaultsNameByPlanAndNumberTwinsInOrder(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"codex","account_key":"`+keyCodex+`","plan":"plus"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyPro+`","plan":"pro"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	out := getAccounts(t, srv, testToken)
	want := keyMaxB + "=Max 1," + keyPro + "=Pro," + keyMaxA + "=Max 2," + keyCodex + "=Plus"
	if got := accountNames(out); got != want {
		t.Fatalf("accounts = %s, want %s (Claude first, then by first seen)", got, want)
	}
}

func TestUsageAccountsRouteAuth(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	admin := mintClient(t, srv, "admin", "admin")
	read := mintClient(t, srv, "read", "read")
	ingest := mintClient(t, srv, "ingest", "ingest")
	put := `{"` + keyMaxA + `":{"name":"Work"}}`
	cases := []struct {
		method, token, body string
		want                int
	}{
		{"GET", "", "", http.StatusUnauthorized},
		{"GET", testToken, "", http.StatusOK},
		{"GET", admin.Token, "", http.StatusOK},
		{"GET", read.Token, "", http.StatusForbidden},
		{"GET", ingest.Token, "", http.StatusForbidden},
		{"PUT", "", put, http.StatusUnauthorized},
		{"PUT", admin.Token, put, http.StatusForbidden},
		{"PUT", read.Token, put, http.StatusForbidden},
		{"PUT", testToken, put, http.StatusOK},
	}
	for _, c := range cases {
		if resp, b := devReq(t, srv, c.method, "/v1/usage/accounts", c.token, c.body); resp.StatusCode != c.want {
			t.Errorf("%s with token %.8q = %d %s, want %d", c.method, c.token, resp.StatusCode, b, c.want)
		}
	}
}

func TestUsageAccountsPutRenamesHidesAndReorders(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"codex","account_key":"`+keyCodex+`","plan":"plus"}`)
	resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken,
		`{"`+keyCodex+`":{"order":0},"`+keyMaxB+`":{"name":"  Work  ","order":1},"`+keyMaxA+`":{"hidden":true}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	var out usageAccountsOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	want := keyCodex + "=Plus," + keyMaxB + "=Work," + keyMaxA + "=Max"
	if got := accountNames(out); got != want {
		t.Fatalf("accounts = %s, want %s", got, want)
	}
	if !out.Accounts[2].Hidden || out.Accounts[1].DefaultName != "Max 1" || *out.Accounts[0].Order != 0 {
		t.Fatalf("accounts = %+v", out.Accounts)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+keyMaxB+`":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset put = %d %s", resp.StatusCode, b)
	}
	got := getAccounts(t, srv, testToken)
	if names := accountNames(got); names != keyCodex+"=Plus,"+keyMaxA+"=Max 1,"+keyMaxB+"=Max 2" {
		t.Fatalf("after reset = %s", names)
	}
}

func TestUsageAccountsPutRejectsUnknownOrBadSettings(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	for _, body := range []string{
		`{"` + keyMaxB + `":{"name":"Ghost"}}`,
		`{"` + keyMaxA + `":{"name":"` + strings.Repeat("x", 25) + `"}}`,
		`{"` + keyMaxA + `":{"name":"a\u0007b"}}`,
		`{"` + keyMaxA + `":{"order":-1}}`,
		`{"` + keyMaxA + `":{"order":1000}}`,
		`{"` + keyMaxA + `":{"colour":"red"}}`,
		`{"` + keyMaxA + `":"Work"}`,
		`[]`,
	} {
		if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	if got := getAccounts(t, srv, testToken); got.Accounts[0].Name != "Max" {
		t.Fatalf("a rejected put changed the name: %+v", got)
	}
}

func TestStaleKeylessAccountLeavesOnceItsToolHasAKeyedOne(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s := newUsageStore()
	s.Put("claude", ToolUsage{UpdatedAt: now})
	s.Put("claude", ToolUsage{Account: keyMaxA, Plan: "max", UpdatedAt: now.Add(time.Minute)})
	both := buildUsageAccounts(s.state(nil, now.Add(2*time.Minute)), nil, nil)
	if len(both) != 2 {
		t.Fatalf("while both are fresh = %+v, want two", both)
	}
	later := buildUsageAccounts(s.state(nil, now.Add(usageStaleTTL+time.Second)), nil, nil)
	if len(later) != 1 || later[0].ID != keyMaxA {
		t.Fatalf("after the keyless one went stale = %+v, want only the keyed account", later)
	}
}

func TestUsageAccountOrderAndNamesSurviveARestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+keyMaxA+`":{"name":"Work"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	_ = app.store.Close()
	app2, srv2 := newDevicesApp(t, db)
	app2.loadUsageAccounts()
	app2.reapplySettings()
	postUsage(t, srv2, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	out := getAccounts(t, srv2, testToken)
	if got := accountNames(out); got != keyMaxB+"=Max,"+keyMaxA+"=Work" {
		t.Fatalf("after restart = %s, want first-seen order and the stored name", got)
	}
	if out.Accounts[0].UpdatedAt != nil || !out.Accounts[0].Stale {
		t.Fatalf("an account with no post since the restart = %+v, want no updated_at and stale", out.Accounts[0])
	}
}

func TestUsageAccountRegistryWritesOnlyOnChange(t *testing.T) {
	kv := &usageSeenKV{}
	r := newUsageAccountRegistry(func() settingsKV { return kv })
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	k := usageKey{"claude", keyMaxA}
	steps := []struct {
		at    time.Duration
		plan  string
		write bool
	}{
		{0, "max", true},
		{time.Minute, "max", false},
		{59 * time.Minute, "max", false},
		{time.Hour, "max", true},
		{time.Hour + time.Minute, "pro", true},
	}
	for _, s := range steps {
		before := kv.puts
		if err := r.observe(k, s.plan, now.Add(s.at)); err != nil {
			t.Fatal(err)
		}
		if wrote := kv.puts > before; wrote != s.write {
			t.Errorf("observe at %v plan %s wrote = %v, want %v", s.at, s.plan, wrote, s.write)
		}
	}
	if got := r.snapshot(now.Add(2 * time.Hour))[keyMaxA]; !got.First.Equal(now) || got.Plan != "pro" {
		t.Fatalf("seen = %+v, want the first sighting kept and the new plan", got)
	}
}

type usageSeenKV struct {
	puts int
	vals map[string]string
}

func (c *usageSeenKV) GetSetting(key string) (string, bool, error) {
	v, ok := c.vals[key]
	return v, ok, nil
}

func (c *usageSeenKV) PutSetting(key, value string) error {
	if c.vals == nil {
		c.vals = map[string]string{}
	}
	c.puts++
	c.vals[key] = value
	return nil
}
