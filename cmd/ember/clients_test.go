package main

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	legacyIngestToken  = clientTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	legacyBoundToken   = clientTokenPrefix + "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	legacyKnobToken    = deviceTokenPrefix + "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	legacyIngestClient = "client-0a0b0c0d"
	legacyBoundClient  = "client-1a2b3c4d"
)

func legacyDevicesBlob(t *testing.T, knobRecord string) string {
	t.Helper()
	records := []string{
		`{"id":"` + legacyIngestClient + `","kind":"client","hw_id":"","name":"old ci","token_sha256":"` + tokenHash(legacyIngestToken) +
			`","config":{},"config_version":0,"created_at":"2026-09-01T00:00:00Z","scopes":["ingest"]}`,
		`{"id":"` + legacyBoundClient + `","kind":"client","hw_id":"","name":"ha","token_sha256":"` + tokenHash(legacyBoundToken) +
			`","config":{"brightness":{"follow_ember":false,"level":0,"floor":0,"startup":0},"pages":null,"home":"","poll_ms":0},` +
			`"config_version":0,"created_at":"2026-09-20T00:00:00Z","scopes":["control","ingest"],"sources":["homeassistant"]}`,
		`{"id":"knob-aabbcc","kind":"cinder-knob","hw_id":"112233aabbcc","name":"Old knob","token_sha256":"` + tokenHash(legacyKnobToken) +
			`","config":{"brightness":{"follow_ember":true,"level":128,"floor":1,"startup":64},"pages":[{"id":"bot","on":true}],"home":"bot","poll_ms":2000},` +
			`"config_version":4,"created_at":"2026-08-01T00:00:00Z"}`,
	}
	if knobRecord != "" {
		records = append(records, knobRecord)
	}
	return `{"epoch":7,"devices":[` + strings.Join(records, ",") + `]}`
}

func storedKnobRecord(t *testing.T, app *App) string {
	t.Helper()
	blob, _, err := app.store.GetSetting(devicesKey)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Devices []json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal([]byte(blob), &st); err != nil || len(st.Devices) != 1 {
		t.Fatalf("stored devices = %s (%v)", blob, err)
	}
	return string(st.Devices[0])
}

func listIDs(t *testing.T, b []byte, key string) map[string]string {
	t.Helper()
	var out map[string][]struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	ids := map[string]string{}
	for _, r := range out[key] {
		ids[r.ID] = r.Kind
	}
	return ids
}

func storedClientHashes(t *testing.T, kv settingsKV) map[string]string {
	t.Helper()
	clients, err := readClients(kv)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range clients {
		out[c.ID] = c.TokenSHA256
	}
	return out
}

func TestClientMigrationMovesLegacyClientsOutOfDevices(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	knob := mintKnob(t, srv, http.StatusCreated)
	legacy := legacyDevicesBlob(t, storedKnobRecord(t, app))
	if err := app.store.PutSetting(devicesKey, legacy); err != nil {
		t.Fatal(err)
	}
	if err := app.devices.load(); err == nil {
		t.Fatal("device registry loaded an unmigrated client record")
	}
	srv.Close()
	_ = app.store.Close()

	app, srv = newDevicesApp(t, db)
	wantHashes := map[string]string{
		legacyIngestClient: tokenHash(legacyIngestToken),
		legacyBoundClient:  tokenHash(legacyBoundToken),
	}
	assertMigrated := func(stage string) {
		t.Helper()
		if got := storedClientHashes(t, app.store); !maps.Equal(got, wantHashes) {
			t.Fatalf("%s: stored client hashes = %v, want %v", stage, got, wantHashes)
		}
		blob, _, _ := app.store.GetSetting(devicesKey)
		if strings.Contains(blob, `"kind":"client"`) || strings.Contains(blob, `"scopes"`) {
			t.Fatalf("%s: devices blob still holds clients: %s", stage, blob)
		}
		if app.devices.epochValue() != 7 {
			t.Fatalf("%s: epoch = %d, want 7 kept", stage, app.devices.epochValue())
		}
		_, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
		if got, want := listIDs(t, b, "devices"), map[string]string{"knob-aabbcc": deviceKindKnob, knob.ID: deviceKindKnob}; !maps.Equal(got, want) {
			t.Fatalf("%s: devices = %v, want %v", stage, got, want)
		}
		_, b = devReq(t, srv, "GET", "/v1/clients", testToken, "")
		if got, want := listIDs(t, b, "clients"), map[string]string{legacyIngestClient: clientKind, legacyBoundClient: clientKind}; !maps.Equal(got, want) {
			t.Fatalf("%s: clients = %v, want %v", stage, got, want)
		}
		if !strings.Contains(string(b), `"scopes":["control","ingest"],"sources":["homeassistant"]`) || !strings.Contains(string(b), `"name":"old ci"`) {
			t.Fatalf("%s: client scopes, sources or names lost: %s", stage, b)
		}
		status := func(source string) string {
			return `{"source":"` + source + `","tool":"gha","session":"1","state":"running"}`
		}
		for _, c := range []struct {
			token, source string
			want          int
		}{
			{legacyIngestToken, "anything", http.StatusOK},
			{legacyBoundToken, "homeassistant", http.StatusOK},
			{legacyBoundToken, "ci", http.StatusForbidden},
		} {
			if resp, b := devReq(t, srv, "POST", "/v1/status", c.token, status(c.source)); resp.StatusCode != c.want {
				t.Fatalf("%s: status from %s = %d %s, want %d", stage, c.source, resp.StatusCode, b, c.want)
			}
		}
		if resp, _ := devReq(t, srv, "POST", "/v1/pomodoro/start", legacyIngestToken, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: ingest token on a control route = %d, want 403", stage, resp.StatusCode)
		}
		if resp, _ := devReq(t, srv, "GET", "/v1/devices", legacyBoundToken, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: non-admin client listed devices = %d, want 403", stage, resp.StatusCode)
		}
		if resp, _ := checkin(t, srv, knob.Token, 1); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: knob checkin = %d", stage, resp.StatusCode)
		}
		if resp, _ := checkin(t, srv, legacyKnobToken, 4); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: legacy knob checkin = %d", stage, resp.StatusCode)
		}
		if resp, _ := checkin(t, srv, legacyIngestToken, 0); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: client token on a knob route = %d, want 401", stage, resp.StatusCode)
		}
	}
	assertMigrated("first run")

	devicesBefore, _, _ := app.store.GetSetting(devicesKey)
	clientsBefore, _, _ := app.store.GetSetting(clientsKey)
	if err := app.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	devicesAfter, _, _ := app.store.GetSetting(devicesKey)
	clientsAfter, _, _ := app.store.GetSetting(clientsKey)
	if devicesAfter != devicesBefore || clientsAfter != clientsBefore {
		t.Fatal("rerun on a migrated store rewrote a blob")
	}
	assertMigrated("rerun")

	srv.Close()
	_ = app.store.Close()
	app, srv = newDevicesApp(t, db)
	assertMigrated("restart")
}

type keyFailingKV struct {
	settingsKV
	failKey string
}

func (f keyFailingKV) PutSetting(key, value string) error {
	if key == f.failKey {
		return errors.New("disk full")
	}
	return f.settingsKV.PutSetting(key, value)
}

func TestClientMigrationIsSafeToRerunAfterACrash(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	legacy := legacyDevicesBlob(t, "")
	if err := app.store.PutSetting(devicesKey, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateClientRecords(keyFailingKV{settingsKV: app.store, failKey: devicesKey}); err == nil {
		t.Fatal("migration hid the failed device write")
	}
	if blob, _, _ := app.store.GetSetting(devicesKey); blob != legacy {
		t.Fatalf("devices blob changed by a failed migration: %s", blob)
	}
	if got := storedClientHashes(t, app.store); len(got) != 2 {
		t.Fatalf("clients written before the crash = %v, want both", got)
	}

	app.devices.kv = func() settingsKV { return keyFailingKV{settingsKV: app.store, failKey: devicesKey} }
	if err := app.loadRegistries(); err == nil {
		t.Fatal("loadRegistries ignored a failed migration")
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/status", legacyIngestToken, `{"source":"ci","tool":"gha","session":"1","state":"running"}`); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("client auth after a failed migration = %d, want 500", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"`+testHwID+`"}`); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("device write after a failed migration = %d, want 500", resp.StatusCode)
	}
	if res := checkClientTokens(app); res.Status != StatusFail {
		t.Fatalf("doctor client_tokens = %+v, want fail", res)
	}

	app.devices.kv = func() settingsKV { return app.store }
	if err := app.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	if got := storedClientHashes(t, app.store); len(got) != 2 {
		t.Fatalf("clients after rerun = %v, want 2 without duplicates", got)
	}
	if resp, b := devReq(t, srv, "POST", "/v1/status", legacyIngestToken, `{"source":"ci","tool":"gha","session":"1","state":"running"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("client auth after rerun = %d %s", resp.StatusCode, b)
	}
	_, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if got := listIDs(t, b, "devices"); !maps.Equal(got, map[string]string{"knob-aabbcc": deviceKindKnob}) {
		t.Fatalf("devices after rerun = %v", got)
	}
}

func TestClientMigrationPrefersTheDeviceBlobsRecord(t *testing.T) {
	app, _ := newDevicesApp(t, "")
	stale := []clientRecord{{ID: legacyIngestClient, Name: "stale", TokenSHA256: tokenHash("ekc_stale"), Scopes: []string{"read"}}}
	if err := writeClients(app.store, stale); err != nil {
		t.Fatal(err)
	}
	if err := app.store.PutSetting(devicesKey, legacyDevicesBlob(t, "")); err != nil {
		t.Fatal(err)
	}
	if n, err := migrateClientRecords(app.store); err != nil || n != 2 {
		t.Fatalf("moved %d (%v), want 2", n, err)
	}
	clients, _ := readClients(app.store)
	if len(clients) != 2 || clients[0].ID != legacyIngestClient || clients[0].TokenSHA256 != tokenHash(legacyIngestToken) {
		t.Fatalf("clients = %+v, want the device blob's record to replace the stale copy", clients)
	}
}

func TestDevicesListShowsNoClients(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	knob := mintKnob(t, srv, http.StatusCreated)
	a := mintClient(t, srv, "ci", "ingest")
	b := mintBoundClient(t, srv, "ha", []string{"homeassistant"}, "ingest")
	_, body := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if got := listIDs(t, body, "devices"); !maps.Equal(got, map[string]string{knob.ID: deviceKindKnob}) {
		t.Fatalf("devices = %v (%s), want the knob only", got, body)
	}
	if strings.Contains(string(body), `"scopes"`) || strings.Contains(string(body), clientIDPrefix) {
		t.Fatalf("devices list leaks clients: %s", body)
	}
	_, body = devReq(t, srv, "GET", "/v1/clients", testToken, "")
	if got := listIDs(t, body, "clients"); !maps.Equal(got, map[string]string{a.ID: clientKind, b.ID: clientKind}) {
		t.Fatalf("clients = %v", got)
	}
	for _, v := range app.devices.list() {
		if v.Kind != deviceKindKnob {
			t.Fatalf("registry list holds %+v", v)
		}
	}
	if res := checkDevices(app); strings.Contains(res.Detail, "client") {
		t.Fatalf("doctor devices = %+v", res)
	}
}

func TestClientWireShapeUnchanged(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	keys := func(b []byte) []string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return slices.Sorted(maps.Keys(m))
	}
	base := []string{"config_version", "created_at", "hw_id", "id", "kind", "last_checkin", "name", "rotated_at", "rotation_pending", "scopes"}
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"client","name":"ci","scopes":["ingest"],"sources":["ci"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint = %d %s", resp.StatusCode, b)
	}
	if got, want := keys(b), slices.Sorted(slices.Values(append(slices.Clone(base), "sources", "token"))); !slices.Equal(got, want) {
		t.Fatalf("mint keys = %v, want %v", got, want)
	}
	var m clientMint
	_ = json.Unmarshal(b, &m)
	if !strings.Contains(string(b), `"hw_id":"","name":"ci","created_at":`) || !strings.Contains(string(b), `"config_version":0,"rotation_pending":false,"rotated_at":null,"last_checkin":null`) {
		t.Fatalf("mint body = %s", b)
	}
	resp, b = devReq(t, srv, "PATCH", "/v1/devices/"+m.ID, testToken, `{"name":"renamed"}`)
	if resp.StatusCode != http.StatusOK || !slices.Equal(keys(b), slices.Sorted(slices.Values(append(slices.Clone(base), "sources")))) || !strings.Contains(string(b), `"name":"renamed"`) {
		t.Fatalf("rename = %d %s", resp.StatusCode, b)
	}
	resp, b = devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	if resp.StatusCode != http.StatusOK || !slices.Equal(keys(b), slices.Sorted(slices.Values(append(slices.Clone(base), "sources", "token")))) {
		t.Fatalf("rotate = %d %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, srv, "GET", "/v1/devices/"+m.ID+"/config", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("client config = %d %s, want 404", resp.StatusCode, b)
	}
	if resp, _ := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", resp.StatusCode)
	}
}

func TestClientChangesLeaveDeviceEpoch(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	mintKnob(t, srv, http.StatusCreated)
	before := app.devices.epochValue()
	m := mintClient(t, srv, "ci", "ingest")
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, "")
	if after := app.devices.epochValue(); after != before {
		t.Fatalf("client changes moved the device epoch %d -> %d", before, after)
	}
}

func TestCorruptClientStoreFailsClosedWithoutTouchingDevices(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	knob := mintKnob(t, srv, http.StatusCreated)
	c := mintClient(t, srv, "ci", "ingest")
	if err := app.store.PutSetting(clientsKey, `{"clients":[{"id":`); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	_ = app.store.Close()

	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	cfg.RateLimit.Disabled = true
	app = NewApp(cfg, &recordingPublisher{}, testLogger())
	if err := app.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.store.Close() })
	if err := app.loadRegistries(); err == nil {
		t.Fatal("loadRegistries hid the corrupt client store")
	}
	srv = httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)

	if resp, _ := checkin(t, srv, knob.Token, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("knob checkin = %d, want 200", resp.StatusCode)
	}
	if resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, ""); resp.StatusCode != http.StatusOK || !strings.Contains(string(b), knob.ID) {
		t.Fatalf("devices list = %d %s", resp.StatusCode, b)
	}
	for _, r := range []struct{ method, path, token, body string }{
		{"POST", "/v1/status", c.Token, `{"source":"ci","tool":"gha","session":"1","state":"running"}`},
		{"GET", "/v1/clients", testToken, ""},
		{"POST", "/v1/devices", testToken, `{"kind":"client","name":"x","scopes":["ingest"]}`},
		{"PATCH", "/v1/devices/" + c.ID, testToken, `{"name":"y"}`},
		{"POST", "/v1/devices/" + c.ID + "/rotate", testToken, ""},
		{"DELETE", "/v1/devices/" + c.ID, testToken, ""},
	} {
		resp, b := devReq(t, srv, r.method, r.path, r.token, r.body)
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(b), "client token store") {
			t.Fatalf("%s %s = %d %s, want 500 naming the client token store", r.method, r.path, resp.StatusCode, b)
		}
	}
	if res := checkClientTokens(app); res.Status != StatusFail {
		t.Fatalf("doctor client_tokens = %+v, want fail", res)
	}
	if res := checkDevices(app); res.Status == StatusFail {
		t.Fatalf("doctor devices = %+v, want not failed", res)
	}
}

func TestAdminClientCannotRenameClients(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	admin := mintClient(t, srv, "admin", "admin")
	other := mintClient(t, srv, "ci", "ingest")
	for _, id := range []string{other.ID, admin.ID} {
		if resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+id, admin.Token, `{"name":"pwned"}`); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("admin client rename of %s = %d %s, want 403", id, resp.StatusCode, b)
		}
	}
	if resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+other.ID, testToken, `{"name":"renamed"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("master rename = %d %s", resp.StatusCode, b)
	}
}
