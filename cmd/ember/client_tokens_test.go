package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type clientMint struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	HwID   string   `json:"hw_id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	Token  string   `json:"token"`
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
