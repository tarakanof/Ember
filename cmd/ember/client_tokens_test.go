package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type clientMint struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	HwID    string   `json:"hw_id"`
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Sources []string `json:"sources"`
	Token   string   `json:"token"`
}

func mintClient(t *testing.T, srv *httptest.Server, name string, scopes ...string) clientMint {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"kind": "client", "name": name, "scopes": scopes})
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, string(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint client status = %d, want 201: %s", resp.StatusCode, b)
	}
	var m clientMint
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMintClientTokenReturnsScopedEkcToken(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "CI runner", "ingest")
	if !strings.HasPrefix(m.Token, clientTokenPrefix) || len(m.Token) != 47 {
		t.Fatalf("token = %q, want ekc_ + 43 chars", m.Token)
	}
	if m.Kind != deviceKindClient || !strings.HasPrefix(m.ID, "client-") || m.HwID != "" || m.Name != "CI runner" {
		t.Fatalf("minted = %+v", m)
	}
	if !slices.Equal(m.Scopes, []string{"ingest"}) {
		t.Fatalf("scopes = %v", m.Scopes)
	}
	resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(b), m.Token) || !strings.Contains(string(b), `"scopes":["ingest"]`) {
		t.Fatalf("list = %d %s", resp.StatusCode, b)
	}
}

func TestMintClientNormalizesScopes(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "ha", "control", "ingest", "control")
	if !slices.Equal(m.Scopes, []string{"control", "ingest"}) {
		t.Fatalf("scopes = %v, want sorted and deduplicated", m.Scopes)
	}
}

func TestMintClientRejectsInvalidBodies(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	cases := map[string]string{
		"no scopes":      `{"kind":"client","name":"x"}`,
		"empty scopes":   `{"kind":"client","name":"x","scopes":[]}`,
		"unknown scope":  `{"kind":"client","name":"x","scopes":["root"]}`,
		"no name":        `{"kind":"client","scopes":["ingest"]}`,
		"hw_id":          `{"kind":"client","name":"x","hw_id":"` + testHwID + `","scopes":["ingest"]}`,
		"knob scopes":    `{"kind":"cinder-knob","hw_id":"` + testHwID + `","scopes":["ingest"]}`,
		"unknown kind":   `{"kind":"robot","name":"x","scopes":["ingest"]}`,
		"unknown fields": `{"kind":"client","name":"x","scopes":["ingest"],"admin":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, b)
			}
		})
	}
}

func TestClientTokenRevokedOnDelete(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "ci", "ingest")
	status := `{"source":"ci","tool":"gha","session":"1","state":"running"}`
	if resp, b := devReq(t, srv, "POST", "/v1/status", m.Token, status); resp.StatusCode != http.StatusOK {
		t.Fatalf("before delete = %d %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.StatusCode, b)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/status", m.Token, status); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after delete = %d, want 401", resp.StatusCode)
	}
}

func TestClientRotateMintsAtOnceAndRevokesOld(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "ci", "ingest")
	resp, b := devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate = %d %q %s", resp.StatusCode, resp.Header.Get("Cache-Control"), b)
	}
	var r clientMint
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Token, clientTokenPrefix) || r.Token == m.Token || r.ID != m.ID {
		t.Fatalf("rotated = %+v", r)
	}
	status := `{"source":"ci","tool":"gha","session":"1","state":"running"}`
	if resp, _ := devReq(t, srv, "POST", "/v1/status", m.Token, status); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token = %d, want 401", resp.StatusCode)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/status", r.Token, status); resp.StatusCode != http.StatusOK {
		t.Fatalf("new token = %d %s", resp.StatusCode, b)
	}
}

func TestClientTokenSurvivesRestart(t *testing.T) {
	db := t.TempDir() + "/s.db"
	app, srv := newDevicesApp(t, db)
	m := mintClient(t, srv, "ci", "ingest")
	srv.Close()
	_ = app.store.Close()
	_, srv2 := newDevicesApp(t, db)
	status := `{"source":"ci","tool":"gha","session":"1","state":"running"}`
	if resp, b := devReq(t, srv2, "POST", "/v1/status", m.Token, status); resp.StatusCode != http.StatusOK {
		t.Fatalf("after restart = %d %s", resp.StatusCode, b)
	}
}

func TestClientHasNoKnobConfigOrStats(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "ci", "ingest")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/devices/" + m.ID + "/config", ""},
		{"PUT", "/v1/devices/" + m.ID + "/config", `{}`},
		{"GET", "/v1/devices/" + m.ID + "/stats", ""},
	} {
		if resp, b := devReq(t, srv, c.method, c.path, testToken, c.body); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404: %s", c.method, c.path, resp.StatusCode, b)
		}
	}
}

func TestClientTokenIsNotADeviceToken(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintClient(t, srv, "everything", "ingest", "control", "read", "admin")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/devices/self/checkin", `{}`},
		{"GET", "/v1/devices/self/config", ""},
		{"GET", "/v1/devices/self/view", ""},
		{"POST", "/v1/nowplaying/control", `{"action":"play"}`},
		{"GET", "/admin/doctor", ""},
	} {
		if resp, b := devReq(t, srv, c.method, c.path, m.Token, c.body); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401: %s", c.method, c.path, resp.StatusCode, b)
		}
	}
}

func TestForgedClientTokenIsUnauthorized(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	mintClient(t, srv, "ci", "ingest")
	resp, _ := devReq(t, srv, "POST", "/v1/status", "ekc_forged", `{"source":"a","tool":"b","session":"c","state":"running"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminClientCannotManageClients(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	admin := mintClient(t, srv, "ops", "admin")
	other := mintClient(t, srv, "ci", "ingest")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/devices", `{"kind":"client","name":"x","scopes":["admin"]}`},
		{"POST", "/v1/devices", `{"kind":"cinder-knob","hw_id":"` + testHwID + `"}`},
		{"POST", "/v1/devices/" + other.ID + "/rotate", ""},
		{"POST", "/v1/devices/" + admin.ID + "/rotate", ""},
		{"DELETE", "/v1/devices/" + other.ID, ""},
	} {
		resp, b := devReq(t, srv, c.method, c.path, admin.Token, c.body)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "EMBER_TOKEN") {
			t.Errorf("%s %s as admin client = %d %s, want 403", c.method, c.path, resp.StatusCode, b)
		}
	}
	status := `{"source":"ci","tool":"gha","session":"1","state":"running"}`
	if resp, b := devReq(t, srv, "POST", "/v1/status", other.Token, status); resp.StatusCode != http.StatusOK {
		t.Fatalf("victim token changed: %d %s", resp.StatusCode, b)
	}
	if resp, _ := devReq(t, srv, "GET", "/v1/devices", admin.Token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin client list = %d, want 200", resp.StatusCode)
	}
}

func TestClientMintCapped(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	for i := range maxClients {
		mintClient(t, srv, "c"+strconv.Itoa(i), "ingest")
	}
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"client","name":"one too many","scopes":["ingest"]}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "at most 64") {
		t.Fatalf("mint past cap = %d %s, want 400", resp.StatusCode, b)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"`+testHwID+`"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("knob mint at client cap = %d %s", resp.StatusCode, b)
	}
}

func TestDoctorDoesNotWarnAboutClients(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	mintClient(t, srv, "ci", "ingest")
	res := checkDevices(app)
	if res.Status != StatusOK || !strings.Contains(res.Detail, "clients=1") {
		t.Fatalf("doctor = %+v", res)
	}
}

func mintBoundClient(t *testing.T, srv *httptest.Server, name string, sources []string, scopes ...string) clientMint {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"kind": "client", "name": name, "scopes": scopes, "sources": sources})
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, string(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint bound client status = %d, want 201: %s", resp.StatusCode, b)
	}
	var m clientMint
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMintClientNormalizesSources(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ha", []string{" homeassistant ", "ci", "ci"}, "ingest")
	if !slices.Equal(m.Sources, []string{"ci", "homeassistant"}) {
		t.Fatalf("sources = %v, want trimmed, sorted and deduplicated", m.Sources)
	}
	resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"sources":["ci","homeassistant"]`) {
		t.Fatalf("list = %d %s", resp.StatusCode, b)
	}
	u := mintClient(t, srv, "ci", "ingest")
	if u.Sources != nil {
		t.Fatalf("unbound sources = %v, want absent", u.Sources)
	}
}

func TestMintClientRejectsInvalidSources(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	many := make([]string, maxClientSources+1)
	for i := range many {
		many[i] = "s" + strconv.Itoa(i)
	}
	tooMany, _ := json.Marshal(map[string]any{"kind": "client", "name": "x", "scopes": []string{"ingest"}, "sources": many})
	cases := map[string]string{
		"empty list":   `{"kind":"client","name":"x","scopes":["ingest"],"sources":[]}`,
		"blank source": `{"kind":"client","name":"x","scopes":["ingest"],"sources":["ci","  "]}`,
		"too long":     `{"kind":"client","name":"x","scopes":["ingest"],"sources":["` + strings.Repeat("a", 65) + `"]}`,
		"too many":     string(tooMany),
		"no ingest":    `{"kind":"client","name":"x","scopes":["control","read"],"sources":["ci"]}`,
		"knob sources": `{"kind":"cinder-knob","hw_id":"` + testHwID + `","sources":["ci"]}`,
		"not a list":   `{"kind":"client","name":"x","scopes":["ingest"],"sources":"ci"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, b)
			}
		})
	}
}

func TestBoundClientRejectsOtherSources(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	if resp, b := devReq(t, srv, "POST", "/v1/status", m.Token, `{"source":"dt-mbp","tool":"claude","session":"1","state":"running"}`); resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), `\"dt-mbp\"`) {
		t.Fatalf("other source = %d %s, want 403 naming the source", resp.StatusCode, b)
	}
	resp, b := devReq(t, srv, "GET", "/state", "", "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(b), "dt-mbp") {
		t.Fatalf("rejected status reached the registry: %s", b)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/status", m.Token, `{"source":" ci ","tool":"gha","session":"1","state":"running"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("own source with spaces = %d %s, want 200", resp.StatusCode, b)
	}
}

func TestMasterDeleteOfOtherSourceUnaffectedByBoundClients(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	status := `{"source":"dt-mbp","tool":"claude","session":"1","state":"running"}`
	if resp, b := devReq(t, srv, "POST", "/v1/status", testToken, status); resp.StatusCode != http.StatusOK {
		t.Fatalf("master post = %d %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/status", m.Token, `{"source":"dt-mbp","tool":"claude","session":"1"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bound delete of other source = %d %s, want 403", resp.StatusCode, b)
	}
	if _, b := devReq(t, srv, "GET", "/state", "", ""); !strings.Contains(string(b), "dt-mbp") {
		t.Fatalf("session gone after a rejected delete: %s", b)
	}
	if resp, b := devReq(t, srv, "DELETE", "/v1/status", testToken, `{"source":"dt-mbp","tool":"claude","session":"1"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master delete = %d %s", resp.StatusCode, b)
	}
}

func TestBoundClientSourcesSurviveRotateAndRestart(t *testing.T) {
	db := t.TempDir() + "/s.db"
	app, srv := newDevicesApp(t, db)
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	resp, b := devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	var r clientMint
	if err := json.Unmarshal(b, &r); err != nil || resp.StatusCode != http.StatusOK || !slices.Equal(r.Sources, []string{"ci"}) {
		t.Fatalf("rotate = %d %s", resp.StatusCode, b)
	}
	srv.Close()
	_ = app.store.Close()
	_, srv2 := newDevicesApp(t, db)
	if resp, b := devReq(t, srv2, "POST", "/v1/status", r.Token, `{"source":"other","tool":"gha","session":"1","state":"running"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other source after restart = %d %s, want 403", resp.StatusCode, b)
	}
	if resp, b := devReq(t, srv2, "POST", "/v1/status", r.Token, `{"source":"ci","tool":"gha","session":"1","state":"running"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("own source after restart = %d %s, want 200", resp.StatusCode, b)
	}
}

func TestLegacyClientRecordWithoutSourcesStaysUnbound(t *testing.T) {
	db := t.TempDir() + "/s.db"
	app, srv := newDevicesApp(t, db)
	token := clientTokenPrefix + strings.Repeat("A", 43)
	legacy := `{"epoch":3,"devices":[{"id":"client-0a0b0c0d","kind":"client","hw_id":"","name":"old ci","token_sha256":"` +
		tokenHash(token) + `","config":{},"config_version":0,"created_at":"2026-09-01T00:00:00Z","scopes":["ingest"]}]}`
	if err := app.store.PutSetting(devicesKey, legacy); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	_ = app.store.Close()
	_, srv2 := newDevicesApp(t, db)
	for _, source := range []string{"ci", "dt-mbp"} {
		body := `{"source":"` + source + `","tool":"gha","session":"1","state":"running"}`
		if resp, b := devReq(t, srv2, "POST", "/v1/status", token, body); resp.StatusCode != http.StatusOK {
			t.Fatalf("legacy token, source %s = %d %s, want 200", source, resp.StatusCode, b)
		}
	}
	if _, b := devReq(t, srv2, "GET", "/v1/devices", testToken, ""); strings.Contains(string(b), `"sources"`) {
		t.Fatalf("legacy client lists sources: %s", b)
	}
}

func TestBoundClientCannotOverwriteAnotherSourcesUsage(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintBoundClient(t, srv, "ci", []string{"ci"}, "ingest")
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"claude","source":"dt-mbp","five_hour":{"used_percent":14}}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("master usage = %d %s", resp.StatusCode, b)
	}
	resp, b := devReq(t, srv, "POST", "/v1/usage", m.Token, `{"tool":"claude","source":"ci","five_hour":{"used_percent":99}}`)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), `\"dt-mbp\"`) {
		t.Fatalf("bound overwrite of dt-mbp usage = %d %s, want 403", resp.StatusCode, b)
	}
	u, ok := app.usage.Get("claude")
	if !ok || u.Source != "dt-mbp" || u.FiveHour == nil || u.FiveHour.UsedPercent != 14 {
		t.Fatalf("claude usage changed: %+v", u)
	}
	for i := range 2 {
		if resp, b := devReq(t, srv, "POST", "/v1/usage", m.Token, `{"tool":"gha","source":"ci"}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("bound usage for a new tool, post %d = %d %s, want 204", i, resp.StatusCode, b)
		}
	}
	if resp, b := devReq(t, srv, "POST", "/v1/usage", testToken, `{"tool":"gha","source":"dt-mbp"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unbound overwrite of ci usage = %d %s, want 204", resp.StatusCode, b)
	}
}

func TestUsagePutIfOwnedIsAtomic(t *testing.T) {
	s := newUsageStore()
	s.Put("claude", ToolUsage{Source: "dt-mbp"})
	if owner, ok := s.PutIfOwned("claude", ToolUsage{Source: "ci"}, []string{"ci"}); ok || owner != "dt-mbp" {
		t.Fatalf("PutIfOwned over dt-mbp = %q %v, want refused", owner, ok)
	}
	if _, ok := s.PutIfOwned("claude", ToolUsage{Source: "dt-mbp"}, nil); !ok {
		t.Fatal("unbound PutIfOwned refused")
	}
	if _, ok := s.PutIfOwned("codex", ToolUsage{Source: "ci"}, []string{"ci"}); !ok {
		t.Fatal("bound PutIfOwned on an absent tool refused")
	}
	var wg sync.WaitGroup
	wins := make(chan string, 2)
	for _, src := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := s.PutIfOwned("race", ToolUsage{Source: src}, []string{src}); ok {
				wins <- src
			}
		}()
	}
	wg.Wait()
	close(wins)
	if n := len(wins); n != 1 {
		t.Fatalf("%d bound writers won an absent tool, want exactly 1", n)
	}
}
