package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	if !out.Accounts[2].Hidden || out.Accounts[1].DefaultName != "Max" || *out.Accounts[0].Order != 0 {
		t.Fatalf("accounts = %+v", out.Accounts)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+idMaxB+`":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset put = %d %s", resp.StatusCode, b)
	}
	got := getAccounts(t, srv, testToken)
	if names := accountNames(got); names != idCodex+"=Plus,"+idMaxA+"=Max 1,"+idMaxB+"=Max" {
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
	if out.Accounts[0].UpdatedAt == nil || !out.Accounts[0].Stale {
		t.Fatalf("an account with no post since the restart = %+v, want its stored updated_at and stale", out.Accounts[0])
	}
}

func TestUsageAccountRestartedFromTheRegistryShowsItsLastSnapshotStale(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","plan":"max","five_hour":{"used_percent":64,"resets_at":4102444800},"seven_day":{"used_percent":41}}`)
	postUsage(t, srv, `{"tool":"codex","source":"codex_stream","account_key":"`+keyCodex+`","plan":"plus","seven_day":{"used_percent":5}}`)
	posted := app.usage.All()[usageKey{"claude", keyMaxA}].UpdatedAt
	_ = app.store.Close()
	app2, _ := newDevicesApp(t, db)
	app2.loadUsageAccounts()
	if n := len(app2.usage.All()); n != 0 {
		t.Fatalf("store after restart = %d entries, want empty", n)
	}
	got := app2.knobUsage(time.Now())
	if got == nil || len(got.Accounts) != 2 {
		t.Fatalf("knob usage after restart = %+v, want both accounts", got)
	}
	a := got.Accounts[0]
	if a.ID != idMaxA || !a.Stale || a.UpdatedAt != posted.Unix() || a.FiveHour == nil || a.FiveHour.Pct != 64 || a.SevenDay == nil || a.SevenDay.Pct != 41 {
		t.Fatalf("claude after restart = %+v, want its last snapshot, stale, updated_at %d", a, posted.Unix())
	}
	if c := got.Accounts[1]; c.ID != idCodex || !c.Stale || c.SevenDay == nil || c.SevenDay.Pct != 5 {
		t.Fatalf("codex after restart = %+v, want its last snapshot, stale", c)
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
		if err := r.observe(k, ToolUsage{Plan: s.plan}, "", now.Add(s.at)); err != nil {
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
		if _, err := s.PutIfOwned("claude", ToolUsage{Account: key, Source: "endpoint", Principal: "client:noisy"}, nil, usageAccountsTotal, now.Add(time.Minute+time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	own, noisy := 0, 0
	for k, u := range s.All() {
		switch {
		case u.Principal == "":
			own++
		case u.Principal == "client:noisy":
			noisy++
			if k.Account == strings.Repeat("k", 16) {
				t.Fatal("the noisy source's oldest account was kept past its cap")
			}
		}
	}
	if own != 3 || noisy != usageAccountsPerTool {
		t.Fatalf("master %d, noisy %d; want 3 untouched and the noisy writer capped at %d", own, noisy, usageAccountsPerTool)
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
	if n := len(app.usage.All()); n != usageAccountsPerWriter {
		t.Fatalf("stored = %d, want the source capped at %d", n, usageAccountsPerWriter)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master after the flood = %d %s", resp.StatusCode, b)
	}
}

func TestUsageHardLimitRejectsScopedWritersButNeverTheMaster(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"dt-mbp","account_key":"`+keyMaxA+`"}`)
	for i := range usageAccountsTotal / usageAccountsPerWriter {
		src := fmt.Sprintf("ci%d", i)
		m := mintBoundClient(t, srv, src, []string{src}, "ingest")
		postBoundUsage(t, srv, m.Token, src, usageAccountsPerWriter)
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

func TestPaddedSourceNamesAreStoredTrimmed(t *testing.T) {
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
		t.Fatalf("stored = %d, want the master's one cap of %d", len(all), usageAccountsPerTool)
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

func TestUsageRegistryCapsEachWriter(t *testing.T) {
	r := newUsageAccountRegistry(func() settingsKV { return nil })
	now := time.Now()
	for i := range 40 {
		k := usageKey{fmt.Sprintf("t%d", i/usageAccountsPerTool), fmt.Sprintf("%016x", i)}
		if err := r.observe(k, ToolUsage{}, "client:ci", now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.snapshot(now.Add(time.Hour))); n != usageAccountsPerWriter {
		t.Fatalf("registry = %d, want one writer capped at %d", n, usageAccountsPerWriter)
	}
}

func TestClientOnASharedSourceCannotEvictTheMastersAccounts(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxB+`","plan":"max"}`)
	first := getAccounts(t, srv, testToken)
	m := mintClient(t, srv, "ci", "ingest")
	for i := range usageAccountsPerTool + 2 {
		body := fmt.Sprintf(`{"tool":"claude","source":"endpoint","account_key":"%016x"}`, i+1)
		if resp, b := devReq(t, srv, "POST", "/v1/usage", m.Token, body); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s = %d %s", body, resp.StatusCode, b)
		}
	}
	all := app.usage.All()
	seen := app.usageSeen.snapshot(time.Now())
	for _, k := range []string{keyMaxA, keyMaxB} {
		if _, ok := all[usageKey{"claude", k}]; !ok {
			t.Fatalf("the master's account %s was evicted from the store", k)
		}
		if _, ok := seen["claude:"+k]; !ok {
			t.Fatalf("the master's account %s was evicted from the registry", k)
		}
	}
	if len(all) != 2+usageAccountsPerTool {
		t.Fatalf("stored = %d, want the master's 2 and the client's own %d", len(all), usageAccountsPerTool)
	}
	after := getAccounts(t, srv, testToken)
	if after.Accounts[0].ID != first.Accounts[0].ID || !after.Accounts[0].FirstSeen.Equal(first.Accounts[0].FirstSeen) {
		t.Fatalf("first account %+v, want %+v unchanged", after.Accounts[0], first.Accounts[0])
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

type registryWriteKV struct {
	settingsKV
	mu   sync.Mutex
	puts map[string]int
}

func (c *registryWriteKV) PutSetting(key, value string) error {
	c.mu.Lock()
	c.puts[key]++
	c.mu.Unlock()
	return c.settingsKV.PutSetting(key, value)
}

func countRegistryWrites(app *App) *registryWriteKV {
	c := &registryWriteKV{settingsKV: app.usageSeen.kv(), puts: map[string]int{}}
	app.usageSeen.kv = func() settingsKV { return c }
	return c
}

func TestUsageRegistryWriteIgnoresTheSourceFlipping(t *testing.T) {
	app, srv := newDevicesApp(t, filepath.Join(t.TempDir(), "s.db"))
	kv := countRegistryWrites(app)
	for i := range 10 {
		src := []string{"statusline", "endpoint"}[i%2]
		postUsage(t, srv, `{"tool":"claude","source":"`+src+`","account_key":"`+keyMaxA+`","plan":"max"}`)
	}
	if n := kv.puts[usageAccountsSeenKey]; n != 1 {
		t.Fatalf("registry writes = %d, want 1 for one account on one plan", n)
	}
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","plan":"pro"}`)
	if n := kv.puts[usageAccountsSeenKey]; n != 2 {
		t.Fatalf("registry writes = %d, want one more for the plan change", n)
	}
}

func TestUsageRegistryRejectsAnObservationOlderThanItsLast(t *testing.T) {
	kv := &usageSeenKV{}
	r := newUsageAccountRegistry(func() settingsKV { return kv })
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	k := usageKey{"claude", keyMaxA}
	if err := r.observe(k, ToolUsage{Plan: "pro"}, "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.observe(k, ToolUsage{Plan: "max"}, "client:ci", now); err != nil {
		t.Fatal(err)
	}
	got := r.snapshot(now.Add(2 * time.Minute))[idMaxA]
	if got.Plan != "pro" || !got.Last.Equal(now.Add(time.Minute)) || got.Principal != "" {
		t.Fatalf("seen = %+v, want the newer observation kept", got)
	}
	if kv.puts != 1 || strings.Contains(kv.vals[usageAccountsSeenKey], `"max"`) {
		t.Fatalf("persisted %d times: %s, want only the newer one", kv.puts, kv.vals[usageAccountsSeenKey])
	}
}

func clientUsagePost(t *testing.T, srv *httptest.Server, token, key string, pct int) int {
	t.Helper()
	body := fmt.Sprintf(`{"tool":"claude","source":"endpoint","account_key":"%s","five_hour":{"used_percent":%d}}`, key, pct)
	resp, _ := devReq(t, srv, "POST", "/v1/usage", token, body)
	return resp.StatusCode
}

func TestClientRepostingTheMastersAccountIsRefused(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`)
	for _, m := range []clientMint{mintClient(t, srv, "open", "ingest"), mintBoundClient(t, srv, "ci", []string{"endpoint"}, "ingest")} {
		if code := clientUsagePost(t, srv, m.Token, keyMaxA, 99); code != http.StatusForbidden {
			t.Fatalf("client repost of the master's account = %d, want 403", code)
		}
	}
	u, ok := app.usage.All()[usageKey{"claude", keyMaxA}]
	if !ok || u.FiveHour.UsedPercent != 14 || u.Principal != "" {
		t.Fatalf("master entry = %+v, want it untouched and still the master's", u)
	}
	if s := app.usageSeen.snapshot(time.Now())[idMaxA]; s.Principal != "" {
		t.Fatalf("registry entry = %+v, want it still the master's", s)
	}
}

func TestClientAtItsCapNeverEvictsAMasterAccountItReposted(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`)
	m := mintClient(t, srv, "open", "ingest")
	clientUsagePost(t, srv, m.Token, keyMaxA, 99)
	for i := range usageAccountsPerTool + 2 {
		if code := clientUsagePost(t, srv, m.Token, fmt.Sprintf("%016x", i+1), 5); code != http.StatusNoContent {
			t.Fatalf("client post %d = %d", i, code)
		}
	}
	if _, ok := app.usage.All()[usageKey{"claude", keyMaxA}]; !ok {
		t.Fatal("the client's cap evicted the master's account from the store")
	}
	if _, ok := app.usageSeen.snapshot(time.Now())[idMaxA]; !ok {
		t.Fatal("the client's cap evicted the master's account from the registry")
	}
}

func TestClientRepostingAnAccountKnownOnlyToTheRegistryIsRefused(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`"}`)
	_ = app.store.Close()
	app2, srv2 := newDevicesApp(t, db)
	app2.loadUsageAccounts()
	m := mintClient(t, srv2, "open", "ingest")
	if code := clientUsagePost(t, srv2, m.Token, keyMaxA, 99); code != http.StatusForbidden {
		t.Fatalf("client post of the master's account after a restart = %d, want 403", code)
	}
}

func TestUsagePostRejectsOversizedModelsAndLabels(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	long := strings.Repeat("x", 33)
	for _, body := range []string{
		`{"tool":"claude","models":{"a":{"used_percent":1},"b":{"used_percent":1},"c":{"used_percent":1},"d":{"used_percent":1},"e":{"used_percent":1}}}`,
		`{"tool":"claude","models":{"Opus 4":{"used_percent":1}}}`,
		`{"tool":"claude","models":{"` + strings.Repeat("o", 25) + `":{"used_percent":1}}}`,
		`{"tool":"claude","five_hour":{"used_percent":1,"reset_label":"` + long + `"}}`,
		`{"tool":"claude","seven_day":{"used_percent":1,"reset_label":"` + long + `"}}`,
		`{"tool":"claude","models":{"opus":{"used_percent":1,"reset_label":"` + long + `"}}}`,
	} {
		if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	postUsage(t, srv, `{"tool":"claude","five_hour":{"used_percent":1,"reset_label":"`+strings.Repeat("x", 32)+`"},"models":{"opus":{"used_percent":1},"sonnet":{"used_percent":1},"haiku-4_5":{"used_percent":1},"x":null}}`)
}

func TestUsageRegistryPersistsATrimmedSnapshotOfBoundedSize(t *testing.T) {
	kv := &usageSeenKV{}
	r := newUsageAccountRegistry(func() settingsKV { return kv })
	loc := time.FixedZone("x", 3*3600)
	now := time.Date(2026, 10, 16, 12, 0, 0, 123456789, loc)
	label := strings.Repeat("L", 32)
	win := func() *UsageWindow {
		return &UsageWindow{UsedPercent: 33.333333333333336, ResetsAt: -9223372036854775807, ResetLabel: label, WindowMinutes: 44640}
	}
	for i := range usageAccountsTotal {
		principal := fmt.Sprintf("client:client-%08x", i/usageAccountsPerWriter)
		tool := fmt.Sprintf("%s%02d", strings.Repeat("t", 30), i/usageAccountsPerTool)
		models := map[string]*UsageWindow{}
		for j := range usageModelsMax {
			models[fmt.Sprintf("%s%d", strings.Repeat("m", 15), j)] = win()
		}
		rep := ToolUsage{FiveHour: win(), SevenDay: win(), Models: models, Source: strings.Repeat("s", 64),
			Account: fmt.Sprintf("%016x", i), Plan: strings.Repeat("p", 24)}
		clampUsageReport(&rep, now)
		if err := r.observe(usageKey{tool, rep.Account}, rep, principal, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.snapshot(now.Add(time.Hour))); n != usageAccountsTotal {
		t.Fatalf("registry = %d, want %d", n, usageAccountsTotal)
	}
	blob := kv.vals[usageAccountsSeenKey]
	for _, leak := range []string{label, strings.Repeat("s", 64), "33.33", "123456", "+03:00", "-9223372036854775807"} {
		if strings.Contains(blob, leak) {
			t.Fatalf("persisted registry keeps %q", leak)
		}
	}
	if !strings.Contains(blob, `"`+strings.Repeat("m", 15)+`3"`) {
		t.Fatal("persisted registry dropped a model the live view can show")
	}
	if len(blob) > 28<<10 {
		t.Fatalf("persisted registry = %d B at 64 worst-case accounts, want at most 28 KiB", len(blob))
	}
}

func TestUsageTwinNumberingIgnoresHiddenAccounts(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	if got := accountNames(getAccounts(t, srv, testToken)); got != idMaxA+"=Max 1,"+idMaxB+"=Max 2" {
		t.Fatalf("accounts = %s", got)
	}
	if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+idMaxA+`":{"hidden":true}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	if got := accountNames(getAccounts(t, srv, testToken)); got != idMaxA+"=Max 1,"+idMaxB+"=Max" {
		t.Fatalf("with Max 1 hidden = %s, want the visible one unnumbered", got)
	}
}

func TestUsageAccountSettingsArePrunedWhenTheAccountExpires(t *testing.T) {
	app, srv := newDevicesApp(t, filepath.Join(t.TempDir(), "s.db"))
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxA+`","plan":"max"}`)
	postUsage(t, srv, `{"tool":"claude","account_key":"`+keyMaxB+`","plan":"max"}`)
	if resp, b := devReq(t, srv, "PUT", "/v1/usage/accounts", testToken, `{"`+idMaxA+`":{"name":"Old"},"`+idMaxB+`":{"name":"Kept"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	old := time.Now().Add(-usageAccountTTL - time.Hour)
	app.usage.mu.Lock()
	u := app.usage.byKey[usageKey{"claude", keyMaxA}]
	u.UpdatedAt = old
	app.usage.byKey[usageKey{"claude", keyMaxA}] = u
	app.usage.mu.Unlock()
	app.usageSeen.mu.Lock()
	s := app.usageSeen.seen[idMaxA]
	s.Last = old
	app.usageSeen.seen[idMaxA] = s
	app.usageSeen.mu.Unlock()
	postUsage(t, srv, `{"tool":"codex","account_key":"`+keyCodex+`"}`)
	prefs := app.cfg.Load().UsageAccounts
	if _, ok := prefs[idMaxA]; ok {
		t.Fatalf("settings = %+v, want the expired account's pruned", prefs)
	}
	if prefs[idMaxB].Name != "Kept" {
		t.Fatalf("settings = %+v, want the live account's kept", prefs)
	}
	blob, _, _ := app.usageSeen.kv().GetSetting(usageAccountsSettingsKey)
	if strings.Contains(blob, keyMaxA) {
		t.Fatalf("stored settings %s still hold the expired account", blob)
	}
}

func ageUsageAccount(t *testing.T, app *App, k usageKey, by time.Duration) {
	t.Helper()
	app.usage.mu.Lock()
	u := app.usage.byKey[k]
	u.UpdatedAt = u.UpdatedAt.Add(-by)
	app.usage.byKey[k] = u
	app.usage.mu.Unlock()
	app.usageSeen.mu.Lock()
	s := app.usageSeen.seen[k.id()]
	s.Last = s.Last.Add(-by)
	app.usageSeen.seen[k.id()] = s
	app.usageSeen.mu.Unlock()
}

func TestBoundClientAndMasterKeepTheirOwnAccountsOfOneTool(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"endpoint"}, "ingest")
	if code := clientUsagePost(t, srv, m.Token, keyMaxB, 40); code != http.StatusNoContent {
		t.Fatalf("bound post of its own account = %d", code)
	}
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`)
	if code := clientUsagePost(t, srv, m.Token, keyMaxB, 41); code != http.StatusNoContent {
		t.Fatalf("bound post after the master posted another account = %d, want 204", code)
	}
	if u := app.usage.All()[usageKey{"claude", keyMaxB}]; u.FiveHour.UsedPercent != 41 {
		t.Fatalf("bound account = %+v, want its new post", u)
	}
}

func TestClientTakesOverTheMastersAccountOnceItIsStale(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`)
	box := mintClient(t, srv, "box", "ingest")
	if code := clientUsagePost(t, srv, box.Token, keyMaxA, 50); code != http.StatusForbidden {
		t.Fatalf("box post while the Mac's entry is fresh = %d, want 403", code)
	}
	ageUsageAccount(t, app, usageKey{"claude", keyMaxA}, usageStaleTTL+time.Second)
	if code := clientUsagePost(t, srv, box.Token, keyMaxA, 50); code != http.StatusNoContent {
		t.Fatalf("box post once the Mac's entry is stale = %d, want 204", code)
	}
	if u := app.usage.All()[usageKey{"claude", keyMaxA}]; u.FiveHour.UsedPercent != 50 || u.Principal != "client:"+box.ID {
		t.Fatalf("entry = %+v, want the box's numbers and the box as owner", u)
	}
	if s := app.usageSeen.snapshot(time.Now())[idMaxA]; s.Principal != "client:"+box.ID {
		t.Fatalf("registry = %+v, want the box as owner", s)
	}
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":60}}`)
	if u := app.usage.All()[usageKey{"claude", keyMaxA}]; u.FiveHour.UsedPercent != 60 || u.Principal != "client:"+box.ID {
		t.Fatalf("entry = %+v, want the Mac's update kept and the box still owner while fresh", u)
	}
}

func TestReMintedTokenAdoptsItsOldAccounts(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	old := mintClient(t, srv, "box", "ingest")
	if code := clientUsagePost(t, srv, old.Token, keyMaxA, 10); code != http.StatusNoContent {
		t.Fatalf("first post = %d", code)
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/devices/"+old.ID, testToken, ""); resp.StatusCode/100 != 2 {
		t.Fatalf("delete = %d %s", resp.StatusCode, b)
	}
	fresh := mintClient(t, srv, "box", "ingest")
	if code := clientUsagePost(t, srv, fresh.Token, keyMaxA, 11); code != http.StatusNoContent {
		t.Fatalf("re-minted token's post = %d, want 204", code)
	}
	if u := app.usage.All()[usageKey{"claude", keyMaxA}]; u.Principal != "client:"+fresh.ID {
		t.Fatalf("entry = %+v, want the new token as owner", u)
	}
}

func TestSecondKeylessTokenWinsOnceTheFirstIsStale(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	a, b := mintClient(t, srv, "a", "ingest"), mintClient(t, srv, "b", "ingest")
	post := func(tok string) int {
		resp, _ := devReq(t, srv, "POST", "/v1/usage", tok, `{"tool":"claude","five_hour":{"used_percent":5}}`)
		return resp.StatusCode
	}
	if code := post(a.Token); code != http.StatusNoContent {
		t.Fatalf("first keyless post = %d", code)
	}
	if code := post(b.Token); code != http.StatusForbidden {
		t.Fatalf("second keyless post while the first is fresh = %d, want 403", code)
	}
	ageUsageAccount(t, app, usageKey{"claude", ""}, usageStaleTTL+time.Second)
	if code := post(b.Token); code != http.StatusNoContent {
		t.Fatalf("second keyless post once the first is stale = %d, want 204", code)
	}
}

func TestMasterUpdatesAFreshClientAccountWithoutTakingIt(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "open", "ingest")
	if code := clientUsagePost(t, srv, m.Token, keyMaxA, 10); code != http.StatusNoContent {
		t.Fatalf("client post = %d", code)
	}
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`","five_hour":{"used_percent":14}}`)
	if u := app.usage.All()[usageKey{"claude", keyMaxA}]; u.FiveHour.UsedPercent != 14 || u.Principal != "client:"+m.ID {
		t.Fatalf("entry = %+v, want the master's numbers, the client still owner", u)
	}
	if code := clientUsagePost(t, srv, m.Token, keyMaxA, 15); code != http.StatusNoContent {
		t.Fatalf("client repost of its own account = %d, want 204", code)
	}
}

func TestUsageRefusalNamesTheOwnerKind(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	postUsage(t, srv, `{"tool":"claude","source":"endpoint","account_key":"`+keyMaxA+`"}`)
	a, b := mintClient(t, srv, "a", "ingest"), mintClient(t, srv, "b", "ingest")
	clientUsagePost(t, srv, a.Token, keyMaxB, 1)
	for _, c := range []struct{ key, want string }{{keyMaxA, "master token"}, {keyMaxB, "another client token"}} {
		resp, body := devReq(t, srv, "POST", "/v1/usage", b.Token, `{"tool":"claude","source":"endpoint","account_key":"`+c.key+`"}`)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), c.want) || strings.Contains(string(body), a.ID) {
			t.Fatalf("refusal = %d %s, want 403 naming the %s and no id", resp.StatusCode, body, c.want)
		}
	}
}

func TestUsagePostClampsResetsAtToASaneRange(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	now := time.Now()
	postUsage(t, srv, `{"tool":"claude","five_hour":{"used_percent":1,"resets_at":-9223372036854775807},"seven_day":{"used_percent":1,"resets_at":9223372036854775807}}`)
	u := app.usage.All()[usageKey{"claude", ""}]
	if lo := now.Add(-25 * time.Hour).Unix(); u.FiveHour.ResetsAt < lo || u.FiveHour.ResetsAt > now.Unix() {
		t.Fatalf("five_hour resets_at = %d, want clamped to a day ago", u.FiveHour.ResetsAt)
	}
	if hi := now.Add(31 * 24 * time.Hour).Unix(); u.SevenDay.ResetsAt > hi || u.SevenDay.ResetsAt < now.Unix() {
		t.Fatalf("seven_day resets_at = %d, want clamped to 30 days ahead", u.SevenDay.ResetsAt)
	}
}
