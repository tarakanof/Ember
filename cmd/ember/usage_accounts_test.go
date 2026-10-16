package main

import (
	"encoding/json"
	"fmt"
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

	idMaxA  = "claude:" + keyMaxA
	idMaxB  = "claude:" + keyMaxB
	idPro   = "claude:" + keyPro
	idCodex = "codex:" + keyCodex
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
	want := idMaxB + "=Max 1," + idPro + "=Pro," + idMaxA + "=Max 2," + idCodex + "=Plus"
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
	put := `{"` + idMaxA + `":{"name":"Work"}}`
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
		`{"`+idCodex+`":{"order":0},"`+idMaxB+`":{"name":"  Work  ","order":1},"`+idMaxA+`":{"hidden":true}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	var out usageAccountsOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	want := idCodex + "=Plus," + idMaxB + "=Work," + idMaxA + "=Max"
	if got := accountNames(out); got != want {
		t.Fatalf("accounts = %s, want %s", got, want)
	}
	if !out.Accounts[2].Hidden || out.Accounts[1].DefaultName != "Max 1" || *out.Accounts[0].Order != 0 {
		t.Fatalf("accounts = %+v", out.Accounts)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+idMaxB+`":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset put = %d %s", resp.StatusCode, b)
	}
	got := getAccounts(t, srv, testToken)
	if names := accountNames(got); names != idCodex+"=Plus,"+idMaxA+"=Max 1,"+idMaxB+"=Max 2" {
		t.Fatalf("after reset = %s", names)
	}
}

func TestUsageAccountsPutRejectsUnknownOrBadSettings(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	for _, body := range []string{
		`{"` + idMaxB + `":{"name":"Ghost"}}`,
		`{"` + idMaxA + `":{"name":"` + strings.Repeat("x", 25) + `"}}`,
		`{"` + idMaxA + `":{"name":"a\u0007b"}}`,
		`{"` + idMaxA + `":{"order":-1}}`,
		`{"` + idMaxA + `":{"order":1000}}`,
		`{"` + idMaxA + `":{"colour":"red"}}`,
		`{"` + idMaxA + `":"Work"}`,
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
	if len(later) != 1 || later[0].ID != idMaxA {
		t.Fatalf("after the keyless one went stale = %+v, want only the keyed account", later)
	}
}

func TestUsageAccountOrderAndNamesSurviveARestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+idMaxA+`":{"name":"Work"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	_ = app.store.Close()
	app2, srv2 := newDevicesApp(t, db)
	app2.loadUsageAccounts()
	app2.reapplySettings()
	postUsage(t, srv2, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	out := getAccounts(t, srv2, testToken)
	if got := accountNames(out); got != idMaxB+"=Max,"+idMaxA+"=Work" {
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
		if err := r.observe(k, s.plan, "endpoint", now.Add(s.at)); err != nil {
			t.Fatal(err)
		}
		if wrote := kv.puts > before; wrote != s.write {
			t.Errorf("observe at %v plan %s wrote = %v, want %v", s.at, s.plan, wrote, s.write)
		}
	}
	if got := r.snapshot(now.Add(2 * time.Hour))[idMaxA]; !got.First.Equal(now) || got.Plan != "pro" {
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

func TestBoundClientCannotAddAnAccountToAnotherSourcesTool(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master usage = %d %s", resp.StatusCode, b)
	}
	for _, key := range []string{keyMaxB, ""} {
		body := `{"tool":"claude","source":"ci","five_hour":{"used_percent":99}`
		if key != "" {
			body += `,"account_key":"` + key + `"`
		}
		if resp, b := devReq(t, srv, "POST", "/v1/usage", m.Token, body+`}`); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("bound post to dt-mbp's tool with key %q = %d %s, want 403", key, resp.StatusCode, b)
		}
	}
	if n := len(app.usage.state(nil, time.Now()).Accounts); n != 1 {
		t.Fatalf("accounts = %d, want only dt-mbp's", n)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/usage", m.Token, `{"tool":"gha","source":"ci","account_key":"`+keyMaxB+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("bound post to a fresh tool = %d %s, want 204", resp.StatusCode, b)
	}
}

func TestSameAccountKeyOnTwoToolsStaysTwoAccounts(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"codex","account_key":"`+keyMaxA+`","plan":"plus"}`)
	if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"codex:`+keyMaxA+`":{"hidden":true}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	out := getAccounts(t, srv, testToken)
	if accountNames(out) != "claude:"+keyMaxA+"=Max,codex:"+keyMaxA+"=Plus" || out.Accounts[0].Hidden || !out.Accounts[1].Hidden {
		t.Fatalf("accounts = %+v, want two distinct ids, only codex hidden", out.Accounts)
	}
	if resp, _ := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+keyMaxA+`":{"hidden":true}}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bare key as id = %d, want 400", resp.StatusCode)
	}
}

func TestUsageStoreCapsAccountsPerToolEvictingTheOldest(t *testing.T) {
	s := newUsageStore()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for i := range usageAccountsPerTool + 2 {
		key := strings.Repeat(string(rune('a'+i)), 16)
		s.Put("claude", ToolUsage{Account: key, UpdatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	all := s.All()
	if len(all) != usageAccountsPerTool {
		t.Fatalf("accounts = %d, want %d", len(all), usageAccountsPerTool)
	}
	for _, gone := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		if _, ok := all[usageKey{"claude", gone}]; ok {
			t.Fatalf("oldest account %s kept", gone)
		}
	}
}

func TestUsagePostIsStampedWithTheReceiveTime(t *testing.T) {
	now := time.Now()
	for _, sent := range []time.Time{now.Add(30 * 24 * time.Hour), now.Add(-6 * 24 * time.Hour), {}} {
		s := newUsageStore()
		s.Put("claude", ToolUsage{Account: keyMaxA, UpdatedAt: now.Add(-time.Hour)})
		if _, err := s.PutIfOwned("codex", ToolUsage{Account: keyCodex, UpdatedAt: sent}, nil, usageAccountsTotal, now); err != nil {
			t.Fatal(err)
		}
		all := s.All()
		if _, ok := all[usageKey{"claude", keyMaxA}]; !ok {
			t.Fatalf("a post dated %v evicted another account", sent)
		}
		if got := all[usageKey{"codex", keyCodex}].UpdatedAt; !got.Equal(now) {
			t.Fatalf("a post dated %v stored %v, want the receive time", sent, got)
		}
	}
}

func TestUsageCapEvictsOnlyTheWritersOwnAccounts(t *testing.T) {
	s := newUsageStore()
	now := time.Now()
	for i := range 3 {
		key := strings.Repeat(string(rune('a'+i)), 16)
		if _, err := s.PutIfOwned("claude", ToolUsage{Account: key, Source: "endpoint"}, nil, usageAccountsTotal, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range usageAccountsPerTool + 3 {
		key := strings.Repeat(string(rune('k'+i)), 16)
		if _, err := s.PutIfOwned("claude", ToolUsage{Account: key, Source: "noisy"}, nil, usageAccountsTotal, now.Add(time.Minute+time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	own, noisy := 0, 0
	for k, u := range s.All() {
		switch {
		case u.Source == "endpoint":
			own++
		case u.Source == "noisy":
			noisy++
			if k.Account == strings.Repeat("k", 16) {
				t.Fatal("the noisy source's oldest account was kept past its cap")
			}
		}
	}
	if own != 3 || noisy != usageAccountsPerTool {
		t.Fatalf("endpoint %d, noisy %d; want 3 untouched and the noisy source capped at %d", own, noisy, usageAccountsPerTool)
	}
}

func postBoundUsage(t *testing.T, srv *httptest.Server, token, source string, n int) (rejected int) {
	t.Helper()
	for i := range n {
		body := fmt.Sprintf(`{"tool":"%s-%d","source":"%s","account_key":"%016x"}`, source, i/usageAccountsPerTool, source, i)
		resp, b := devReq(t, srv, "POST", "/v1/usage", token, body)
		switch resp.StatusCode {
		case http.StatusNoContent:
		case http.StatusInsufficientStorage:
			rejected++
		default:
			t.Fatalf("%s = %d %s", body, resp.StatusCode, b)
		}
	}
	return rejected
}

func TestOneSourceCannotFillTheStoreAcrossMadeUpTools(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	if r := postBoundUsage(t, srv, m.Token, "ci", usageAccountsTotal); r != 0 {
		t.Fatalf("%d rejected, want the source's own oldest replaced instead", r)
	}
	if n := len(app.usage.All()); n != usageAccountsPerSource {
		t.Fatalf("stored = %d, want the source capped at %d", n, usageAccountsPerSource)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master after the flood = %d %s", resp.StatusCode, b)
	}
}

func TestUsageHardLimitRejectsScopedWritersButNeverTheMaster(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`"}`)
	for i := range usageAccountsTotal / usageAccountsPerSource {
		src := fmt.Sprintf("ci%d", i)
		m := mintBoundClient(t, srv, src, []string{src}, "ingest")
		postBoundUsage(t, srv, m.Token, src, usageAccountsPerSource)
	}
	late := mintBoundClient(t, srv, "late", []string{"late"}, "ingest")
	if r := postBoundUsage(t, srv, late.Token, "late", 1); r != 1 {
		t.Fatalf("a scoped write past the limit was not refused")
	}
	if _, ok := app.usage.All()[usageKey{"claude", keyMaxA}]; !ok {
		t.Fatal("the master's account was evicted")
	}
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxB+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master past the limit = %d %s, want 204", resp.StatusCode, b)
	}
}

func TestPaddedSourceNamesShareOneCap(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	for i := range 3 * usageAccountsPerTool {
		src := []string{"ci", "ci ", " ci"}[i%3]
		body := fmt.Sprintf(`{"tool":"claude","source":%q,"account_key":"%016x"}`, src, i)
		if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, body); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s = %d %s", body, resp.StatusCode, b)
		}
	}
	all := app.usage.All()
	if len(all) != usageAccountsPerTool {
		t.Fatalf("stored = %d, want one cap of %d for ci however it is padded", len(all), usageAccountsPerTool)
	}
	for _, u := range all {
		if u.Source != "ci" {
			t.Fatalf("stored source %q, want it trimmed", u.Source)
		}
	}
	if n := len(app.usageSeen.snapshot(time.Now())); n != usageAccountsPerTool {
		t.Fatalf("registry = %d, want %d", n, usageAccountsPerTool)
	}
}

func TestUsageRegistryCapsEachSource(t *testing.T) {
	r := newUsageAccountRegistry(func() settingsKV { return nil })
	now := time.Now()
	for i := range 40 {
		k := usageKey{fmt.Sprintf("t%d", i/usageAccountsPerTool), fmt.Sprintf("%016x", i)}
		if err := r.observe(k, "", "ci", now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.snapshot(now.Add(time.Hour))); n != usageAccountsPerSource {
		t.Fatalf("registry = %d, want one source capped at %d", n, usageAccountsPerSource)
	}
}

func TestStaleEntryFromAnotherSourceDoesNotLockOutTheMasterToken(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	app.usage.Put("claude", ToolUsage{Account: keyMaxA, Source: "ci", UpdatedAt: time.Now().Add(-time.Hour)})
	postUsage(t, srv, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxB+`"}`)
	postUsage(t, srv, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`"}`)
	if u := app.usage.All()[usageKey{"claude", keyMaxA}]; u.Source != "dt-mbp" {
		t.Fatalf("entry = %+v, want the master's post to replace the stale ci one", u)
	}
}

func TestUsageStoreGetReturnsTheToolsHighestFiveHourAccount(t *testing.T) {
	s := newUsageStore()
	now := time.Now()
	s.Put("claude", ToolUsage{Account: keyMaxA, FiveHour: &UsageWindow{UsedPercent: 90}, UpdatedAt: now.Add(-time.Minute)})
	s.Put("claude", ToolUsage{Account: keyMaxB, FiveHour: &UsageWindow{UsedPercent: 20}, UpdatedAt: now})
	if u, ok := s.Get("claude"); !ok || u.Account != keyMaxA {
		t.Fatalf("Get = %+v, want the 90%% account", u)
	}
}
