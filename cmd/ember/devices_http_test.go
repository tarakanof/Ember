package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testHwID = "3cdc7561fc8c"

func newDevicesApp(t *testing.T, dbPath string) (*App, *httptest.Server) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	cfg.RateLimit.Disabled = true
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "s.db")
	}
	if err := app.ensureStore(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.store.Close() })
	app.devices.load()
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return app, srv
}

func devReq(t *testing.T, srv *httptest.Server, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

type mintResp struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Name  string `json:"name"`
	HwID  string `json:"hw_id"`
	Kind  string `json:"kind"`
}

func mintKnob(t *testing.T, srv *httptest.Server, wantStatus int) mintResp {
	t.Helper()
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken,
		`{"kind":"cinder-knob","hw_id":"`+testHwID+`","name":"Desk knob"}`)
	if resp.StatusCode != wantStatus {
		t.Fatalf("mint status = %d, want %d: %s", resp.StatusCode, wantStatus, b)
	}
	var m mintResp
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func checkin(t *testing.T, srv *httptest.Server, token string, version int) (*http.Response, map[string]any) {
	t.Helper()
	resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", token,
		`{"fw":"0.5.0","ip":"192.168.0.39","rssi":-58,"heap_internal_free":47104,"heap_internal_largest":31744,"uptime_s":812,"config_version":`+strconv.Itoa(version)+`}`)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp, out
}

func TestDevicesMintReturnsTokenAndID(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	if m.ID != "knob-61fc8c" {
		t.Errorf("id = %q, want knob-61fc8c", m.ID)
	}
	if !strings.HasPrefix(m.Token, "ekd_") || len(m.Token) > 64 {
		t.Errorf("token = %q, want ekd_ prefix and <= 64 chars", m.Token)
	}
	if m.Name != "Desk knob" || m.HwID != testHwID || m.Kind != "cinder-knob" {
		t.Errorf("mint body = %+v", m)
	}
}

func TestDevicesListNeverShowsSecrets(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d", resp.StatusCode)
	}
	if bytes.Contains(b, []byte(m.Token)) || bytes.Contains(b, []byte("token")) || bytes.Contains(b, []byte(tokenHash(m.Token))) {
		t.Fatalf("list leaks a secret: %s", b)
	}
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Devices) != 1 {
		t.Fatalf("list body = %s (%v)", b, err)
	}
	if out.Devices[0]["last_checkin"] != nil {
		t.Errorf("last_checkin before first checkin = %v, want null", out.Devices[0]["last_checkin"])
	}
}

func TestDevicesStoreHoldsHashOnly(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	blob, ok, err := app.store.GetSetting(devicesKey)
	if err != nil || !ok {
		t.Fatalf("store row missing: ok=%v err=%v", ok, err)
	}
	if strings.Contains(blob, m.Token) || strings.Contains(blob, strings.TrimPrefix(m.Token, "ekd_")) {
		t.Fatal("store holds the plaintext token")
	}
	sum := sha256.Sum256([]byte(m.Token))
	if !strings.Contains(blob, hex.EncodeToString(sum[:])) {
		t.Fatal("store lacks the token's SHA-256")
	}
}

func TestDevicesMintRejectsBadBody(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	cases := map[string]string{
		"bad kind":       `{"kind":"toaster","hw_id":"3cdc7561fc8c"}`,
		"missing kind":   `{"hw_id":"3cdc7561fc8c"}`,
		"short hw_id":    `{"kind":"cinder-knob","hw_id":"61fc8c"}`,
		"non-hex hw_id":  `{"kind":"cinder-knob","hw_id":"3cdc7561fcxz"}`,
		"long name":      `{"kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"` + strings.Repeat("n", 65) + `"}`,
		"unknown field":  `{"kind":"cinder-knob","hw_id":"3cdc7561fc8c","token":"x"}`,
		"not an object":  `[]`,
		"trailing value": `{"kind":"cinder-knob","hw_id":"3cdc7561fc8c"}{}`,
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

func TestDevicesMintNormalisesHwIDAndDefaultsName(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"3C:DC:75:61:FC:8C"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
	var m mintResp
	_ = json.Unmarshal(b, &m)
	if m.HwID != testHwID || m.ID != "knob-61fc8c" || m.Name != "Knob 61FC8C" {
		t.Fatalf("mint = %+v", m)
	}
}

func TestDevicesReprovisionRevokesOldTokenKeepsConfig(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	first := mintKnob(t, srv, http.StatusCreated)
	if resp, b := devReq(t, srv, "PUT", "/v1/devices/"+first.ID+"/config", testToken, `{"poll_ms":5000}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("config put = %d: %s", resp.StatusCode, b)
	}
	second := mintKnob(t, srv, http.StatusOK)
	if second.ID != first.ID || second.Token == first.Token {
		t.Fatalf("re-provision = %+v, first = %+v", second, first)
	}
	if resp, _ := checkin(t, srv, first.Token, 0); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after re-provision = %d, want 401", resp.StatusCode)
	}
	resp, out := checkin(t, srv, second.Token, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new token checkin = %d", resp.StatusCode)
	}
	cfg := out["config"].(map[string]any)
	if cfg["poll_ms"] != float64(5000) {
		t.Fatalf("config after re-provision = %v, want poll_ms kept", cfg)
	}
}

func TestDevicesPersistAcrossRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	if resp, _ := devReq(t, srv1, "PUT", "/v1/devices/"+m.ID+"/config", testToken, `{"home":"weather"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("config put = %d", resp.StatusCode)
	}
	epoch := app1.devices.epochValue()
	srv1.Close()
	_ = app1.store.Close()

	app2, srv2 := newDevicesApp(t, db)
	resp, out := checkin(t, srv2, m.Token, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin after restart = %d", resp.StatusCode)
	}
	if out["config"].(map[string]any)["home"] != "weather" || out["config_version"] != float64(2) {
		t.Fatalf("checkin after restart = %v", out)
	}
	if app2.devices.epochValue() != epoch {
		t.Fatalf("epoch after restart = %d, want %d", app2.devices.epochValue(), epoch)
	}
}

func TestDeviceCheckinSendsConfigOnlyWhenStale(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, out := checkin(t, srv, m.Token, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d", resp.StatusCode)
	}
	if out["config_version"] != float64(1) || out["config"] == nil {
		t.Fatalf("stale checkin = %v, want version 1 with config", out)
	}
	if _, ok := out["new_token"]; ok {
		t.Fatal("new_token outside a rotation")
	}
	_, out = checkin(t, srv, m.Token, 1)
	if _, ok := out["config"]; ok || out["config_version"] != float64(1) {
		t.Fatalf("current checkin = %v, want version only", out)
	}
	d := app.devices.list()[0]
	if d.LastCheckin == nil || d.LastCheckin.FW != "0.5.0" || d.LastCheckin.RSSI != -58 ||
		d.LastCheckin.HeapInternalFree != 47104 || d.LastCheckin.UptimeS != 812 || d.LastCheckin.AppliedVersion != 1 {
		t.Fatalf("last checkin = %+v", d.LastCheckin)
	}
}

func TestDeviceCheckinFallsBackToRemoteIP(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	if resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.5.0"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	if got := app.devices.list()[0].LastCheckin.IP; got != "127.0.0.1" {
		t.Fatalf("ip = %q, want remote addr", got)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"ip":"not-an-ip"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad ip checkin = %d, want 400", resp.StatusCode)
	}
}

func TestDeviceSelfConfig(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "GET", "/v1/devices/self/config", m.Token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self config = %d", resp.StatusCode)
	}
	var out struct {
		Version int          `json:"config_version"`
		Config  knobSettings `json:"config"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != 1 || out.Config.PollMS != 2000 {
		t.Fatalf("self config = %s", b)
	}
}

func TestDeviceConfigPutMergesAndBumpsVersion(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	path := "/v1/devices/" + m.ID + "/config"
	epoch0 := app.devices.epochValue()

	resp, b := devReq(t, srv, "PUT", path, testToken, `{"poll_ms":3000,"brightness":{"follow_ember":false,"level":100,"floor":5,"startup":80}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d: %s", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Ember-Config-Version") != "2" {
		t.Fatalf("version header = %q, want 2", resp.Header.Get("X-Ember-Config-Version"))
	}
	var got knobSettings
	_ = json.Unmarshal(b, &got)
	if got.PollMS != 3000 || got.Brightness.Level != 100 || got.Home != "bot" || len(got.Pages) != 4 {
		t.Fatalf("merged = %+v", got)
	}
	if app.devices.epochValue() == epoch0 {
		t.Fatal("epoch did not move on config change")
	}

	epoch1 := app.devices.epochValue()
	resp, _ = devReq(t, srv, "PUT", path, testToken, `{}`)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Ember-Config-Version") != "2" || app.devices.epochValue() != epoch1 {
		t.Fatalf("empty put: status %d version %q epoch moved %v", resp.StatusCode, resp.Header.Get("X-Ember-Config-Version"), app.devices.epochValue() != epoch1)
	}

	resp, b = devReq(t, srv, "GET", path, testToken, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Ember-Config-Version") != "2" {
		t.Fatalf("get = %d %q", resp.StatusCode, resp.Header.Get("X-Ember-Config-Version"))
	}
	_ = json.Unmarshal(b, &got)
	if got.PollMS != 3000 {
		t.Fatalf("get after put = %+v", got)
	}
}

func TestDeviceConfigPutRejectsInvalidMergedResult(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	path := "/v1/devices/" + m.ID + "/config"
	_, before := devReq(t, srv, "GET", path, testToken, "")
	for _, body := range []string{
		`{"poll_ms":50}`,
		`{"pages":[{"id":"bot","on":false}]}`,
		`{"home":"nope"}`,
		`{"brightness":{"floor":200}}`,
		`[]`,
		`{"poll_ms":"fast"}`,
	} {
		resp, b := devReq(t, srv, "PUT", path, testToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %s = %d, want 400: %s", body, resp.StatusCode, b)
		}
	}
	if _, after := devReq(t, srv, "GET", path, testToken, ""); !bytes.Equal(before, after) {
		t.Fatalf("config changed by rejected puts:\n before %s\n after  %s", before, after)
	}
	if v := app.devices.list()[0].ConfigVersion; v != 1 {
		t.Fatalf("version after rejected puts = %d, want 1", v)
	}
	if resp, _ := devReq(t, srv, "PUT", "/v1/devices/knob-000000/config", testToken, `{}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown device put = %d, want 404", resp.StatusCode)
	}
}

func TestDevicePatchRenames(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+m.ID, testToken, `{"name":"Kitchen knob"}`)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"name":"Kitchen knob"`)) {
		t.Fatalf("patch = %d: %s", resp.StatusCode, b)
	}
	for _, body := range []string{`{"name":"  "}`, `{"name":"x","hw_id":"y"}`} {
		if resp, _ := devReq(t, srv, "PATCH", "/v1/devices/"+m.ID, testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("patch %s = %d, want 400", body, resp.StatusCode)
		}
	}
	if resp, _ := devReq(t, srv, "PATCH", "/v1/devices/knob-000000", testToken, `{"name":"x"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown patch = %d, want 404", resp.StatusCode)
	}
}

func TestDeviceRotateDeliversNewTokenAndRetiresOldOnFirstUse(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	epoch0 := app.devices.epochValue()
	resp, b := devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	if resp.StatusCode != http.StatusAccepted || !bytes.Contains(b, []byte(`"rotation_pending":true`)) {
		t.Fatalf("rotate = %d: %s", resp.StatusCode, b)
	}
	if app.devices.epochValue() == epoch0 {
		t.Fatal("epoch did not move on rotate")
	}

	resp, out := checkin(t, srv, m.Token, 1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin with old token during rotation = %d", resp.StatusCode)
	}
	newTok, _ := out["new_token"].(string)
	if !strings.HasPrefix(newTok, "ekd_") || newTok == m.Token {
		t.Fatalf("new_token = %q", newTok)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/pomodoro/stop", m.Token, ""); resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("old token rejected before the new one was used")
	}

	resp, out = checkin(t, srv, newTok, 1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin with new token = %d", resp.StatusCode)
	}
	if _, ok := out["new_token"]; ok {
		t.Fatal("new_token repeated after the new token was used")
	}
	if resp, _ := checkin(t, srv, m.Token, 1); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after first use of new = %d, want 401", resp.StatusCode)
	}
	if app.devices.list()[0].RotationPending {
		t.Fatal("rotation still pending after promotion")
	}
}

func TestDeviceRotateRedeliversSamePendingToken(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out1 := checkin(t, srv, m.Token, 1)
	_, out2 := checkin(t, srv, m.Token, 1)
	t1, _ := out1["new_token"].(string)
	t2, _ := out2["new_token"].(string)
	if t1 == "" || t1 != t2 {
		t.Fatalf("redelivery: %q then %q, want the same token", t1, t2)
	}
	if resp, _ := checkin(t, srv, t1, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("pending token = %d, want 200", resp.StatusCode)
	}
}

func TestDeviceRerotateMintsFreshPendingToken(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out := checkin(t, srv, m.Token, 1)
	t1 := out["new_token"].(string)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out = checkin(t, srv, m.Token, 1)
	t2, _ := out["new_token"].(string)
	if t2 == "" || t2 == t1 {
		t.Fatalf("second rotation new_token = %q, want fresh (first %q)", t2, t1)
	}
	if resp, _ := checkin(t, srv, t2, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("second rotation token = %d, want 200", resp.StatusCode)
	}
}

func TestDeviceRotateRemintsPendingTokenAfterRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	devReq(t, srv1, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out := checkin(t, srv1, m.Token, 1)
	t1 := out["new_token"].(string)
	srv1.Close()
	_ = app1.store.Close()

	_, srv2 := newDevicesApp(t, db)
	_, out = checkin(t, srv2, m.Token, 1)
	t2, _ := out["new_token"].(string)
	if t2 == "" || t2 == t1 {
		t.Fatalf("after restart new_token = %q, want a fresh one", t2)
	}
	if resp, _ := checkin(t, srv2, t1, 1); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-restart pending token = %d, want 401", resp.StatusCode)
	}
	if resp, _ := checkin(t, srv2, t2, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-minted pending token = %d, want 200", resp.StatusCode)
	}
}

func TestDeviceRotationPromotionBumpsEpoch(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out := checkin(t, srv, m.Token, 1)
	e0 := app.devices.epochValue()
	checkin(t, srv, out["new_token"].(string), 1)
	if app.devices.epochValue() == e0 {
		t.Fatal("epoch did not move on rotation promotion")
	}
}

func TestDeviceConfigPutMergesNestedObjects(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	path := "/v1/devices/" + m.ID + "/config"
	resp, b := devReq(t, srv, "PUT", path, testToken, `{"brightness":{"level":100,"floor":5}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d: %s", resp.StatusCode, b)
	}
	var got knobSettings
	_ = json.Unmarshal(b, &got)
	want := knobBrightness{FollowEmber: true, Level: 100, Floor: 5, Startup: 153}
	if got.Brightness != want || got.Bot.SleepyAfterS != 300 {
		t.Fatalf("nested merge = %+v, want %+v", got, want)
	}
	resp, b = devReq(t, srv, "PUT", path, testToken, `{"pages":[{"id":"weather","on":true},{"id":"bot"}],"home":"weather"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pages put = %d: %s", resp.StatusCode, b)
	}
	_ = json.Unmarshal(b, &got)
	wantPages := []knobPage{{ID: "weather", On: true}, {ID: "bot", On: false}}
	if len(got.Pages) != 2 || got.Pages[0] != wantPages[0] || got.Pages[1] != wantPages[1] {
		t.Fatalf("pages replaced = %+v, want %+v", got.Pages, wantPages)
	}
	for _, body := range []string{`{"brightness":{"lvl":1}}`, `{"colour":"red"}`} {
		if resp, _ := devReq(t, srv, "PUT", path, testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %s = %d, want 400 (unknown field)", body, resp.StatusCode)
		}
	}
}

func TestDeviceConfigKeepsUnknownWellFormedPageIDs(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "PUT", "/v1/devices/"+m.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"clock_v2","on":true}],"home":"clock_v2"}`)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`{"id":"clock_v2","on":true}`)) {
		t.Fatalf("put = %d: %s", resp.StatusCode, b)
	}
}

func TestDeviceRegistryLoadFailureRefusesWrites(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	const bad = `{"devices":[{"id":`
	if err := app.store.PutSetting(devicesKey, bad); err != nil {
		t.Fatal(err)
	}
	if err := app.devices.load(); err == nil {
		t.Fatal("load of a corrupt blob succeeded")
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"`+testHwID+`"}`); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("mint after failed load = %d, want 500", resp.StatusCode)
	}
	if blob, _, _ := app.store.GetSetting(devicesKey); blob != bad {
		t.Fatalf("stored blob overwritten: %q", blob)
	}
	if got := checkDevices(app); got.Status != StatusFail || !strings.Contains(got.Detail, "load failed") {
		t.Fatalf("doctor = %+v, want fail", got)
	}
}

func TestDeviceRotateRetiresOldTokenAfterGrace(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	app.devices.now = func() time.Time { return now }
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out := checkin(t, srv, m.Token, 1)
	newTok := out["new_token"].(string)

	now = now.Add(deviceRotationGrace + time.Second)
	if resp, _ := checkin(t, srv, m.Token, 1); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after grace = %d, want 401", resp.StatusCode)
	}
	if resp, _ := checkin(t, srv, newTok, 1); resp.StatusCode != http.StatusOK {
		t.Fatalf("pending token after grace = %d, want 200", resp.StatusCode)
	}
}

func TestDeviceDeleteRevokes(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	epoch0 := app.devices.epochValue()
	if resp, _ := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
	if app.devices.epochValue() == epoch0 {
		t.Fatal("epoch did not move on delete")
	}
	if resp, _ := checkin(t, srv, m.Token, 1); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("checkin after delete = %d, want 401", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "DELETE", "/v1/devices/"+m.ID, testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rotate after delete = %d, want 404", resp.StatusCode)
	}
}

func TestDevicesEpochHeaderOnState(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	epoch := func() string {
		resp, _ := devReq(t, srv, "GET", "/state", "", "")
		return resp.Header.Get("X-Ember-Devices-Epoch")
	}
	e0 := epoch()
	if e0 == "" {
		t.Fatal("/state lacks X-Ember-Devices-Epoch")
	}
	m := mintKnob(t, srv, http.StatusCreated)
	e1 := epoch()
	devReq(t, srv, "PUT", "/v1/devices/"+m.ID+"/config", testToken, `{"poll_ms":4000}`)
	e2 := epoch()
	checkin(t, srv, m.Token, 0)
	e3 := epoch()
	if e1 == e0 || e2 == e1 {
		t.Fatalf("epoch did not move: %s %s %s", e0, e1, e2)
	}
	if e3 != e2 {
		t.Fatalf("checkin moved the epoch: %s -> %s", e2, e3)
	}
}

func TestDeviceRoutesFailClosedWithoutOwnerToken(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	app.updateConfig(func(c *Config) { c.Auth.StatusToken = "" })
	if resp, _ := checkin(t, srv, m.Token, 0); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("checkin with EMBER_TOKEN unset = %d, want 401", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/pomodoro/stop", m.Token, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pomodoro with EMBER_TOKEN unset = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceRoutesAreRateLimited(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	app.updateConfig(func(c *Config) {
		c.RateLimit.Disabled = false
		c.RateLimit.Burst = 1
		c.RateLimit.RefillPerSec = 0.001
	})
	checkin(t, srv, "ekd_wrong", 0)
	if resp, _ := checkin(t, srv, m.Token, 0); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("checkin over budget = %d, want 429", resp.StatusCode)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/pomodoro/stop", m.Token, ""); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pomodoro over budget = %d, want 429", resp.StatusCode)
	}
}

func TestDeviceTokensNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	app, srv := newDevicesApp(t, "")
	app.logger = captureLogger(&buf)
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	_, out := checkin(t, srv, m.Token, 0)
	newTok := out["new_token"].(string)
	checkin(t, srv, newTok, 0)
	checkin(t, srv, m.Token, 0)
	mintKnob(t, srv, http.StatusOK)
	logs := buf.String()
	for _, secret := range []string{m.Token, newTok, tokenHash(m.Token), tokenHash(newTok)} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain a token or its hash")
		}
	}
	if !strings.Contains(logs, "device provisioned") {
		t.Fatalf("expected a provisioning log line, got %s", logs)
	}
}

func TestDoctorReportsDevices(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	app.devices.now = func() time.Time { return now }
	if got := checkDevices(app); got.Status != StatusOK || got.Detail != "registered=0" {
		t.Fatalf("empty = %+v", got)
	}
	m := mintKnob(t, srv, http.StatusCreated)
	if got := checkDevices(app); got.Status != StatusWarn || !strings.Contains(got.Detail, "knob-61fc8c never checked in") {
		t.Fatalf("never seen = %+v", got)
	}
	checkin(t, srv, m.Token, 1)
	now = now.Add(12 * time.Second)
	if got := checkDevices(app); got.Status != StatusOK || !strings.Contains(got.Detail, "knob-61fc8c seen=12s ago") {
		t.Fatalf("seen = %+v", got)
	}
	now = now.Add(deviceStaleAfter)
	if got := checkDevices(app); got.Status != StatusWarn {
		t.Fatalf("stale = %+v", got)
	}
}

// cinder#23: the panel link clock and its fallback ride the checkin into the record.
func TestDeviceCheckinRecordsTheDisplayLink(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	if resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.2","link_mhz":40,"link_fallback":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	got := app.devices.list()[0].LastCheckin
	if got.LinkMHz != 40 || !got.LinkFallback {
		t.Fatalf("link = %d MHz, fallback %v", got.LinkMHz, got.LinkFallback)
	}
	if resp, _ := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"link_mhz":-1}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad link_mhz = %d, want 400", resp.StatusCode)
	}
	devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.1"}`)
	if got := app.devices.list()[0].LastCheckin; got.LinkMHz != 0 || got.LinkFallback {
		t.Fatalf("older firmware: link = %d, %v", got.LinkMHz, got.LinkFallback)
	}
}
