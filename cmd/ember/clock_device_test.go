package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

const testClockUID = "e868e705ffb8"

type clockStub struct {
	*httptest.Server
	mu        sync.Mutex
	uid       string
	board     string
	apps      []map[string]any
	orderPuts []string
	orderFail bool
	appsFail  bool
	onDevice  func()
	orderHold chan struct{}
	orderIn   chan struct{}
}

func newClockStub(t *testing.T) *clockStub {
	t.Helper()
	s := &clockStub{uid: testClockUID, board: "awtrixng", apps: []map[string]any{
		{"name": "time", "enabled": true, "inLoop": true, "origin": "builtin"},
		{"name": "date", "enabled": true, "inLoop": true, "origin": "builtin"},
		{"name": "hum", "enabled": false, "inLoop": false, "origin": "builtin"},
		{"name": "ember", "enabled": true, "inLoop": true, "origin": "pushed"},
	}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *clockStub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && r.URL.Path == "/api/v1/apps/order" {
		s.mu.Lock()
		hold, in := s.orderHold, s.orderIn
		s.mu.Unlock()
		if hold != nil {
			in <- struct{}{}
			<-hold
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/device":
		if s.onDevice != nil {
			s.onDevice()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uid": s.uid, "boardType": s.board, "version": "1.0.13", "uptimeSeconds": 42,
			"wifiRssi": -61, "ipAddress": "192.0.2.66",
		})
	case "GET /api/v1/apps":
		if s.appsFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(s.apps)
	case "PUT /api/v1/apps/order":
		b, _ := io.ReadAll(r.Body)
		s.orderPuts = append(s.orderPuts, string(b))
		if s.orderFail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		var body struct {
			Order    []string `json:"order"`
			Disabled []string `json:"disabled"`
		}
		_ = json.Unmarshal(b, &body)
		byName := map[string]map[string]any{}
		for _, a := range s.apps {
			byName[a["name"].(string)] = a
		}
		var next []map[string]any
		for _, n := range body.Order {
			if a, ok := byName[n]; ok {
				a["enabled"] = true
				next = append(next, a)
				delete(byName, n)
			}
		}
		for _, a := range s.apps {
			if _, ok := byName[a["name"].(string)]; ok {
				next = append(next, a)
			}
		}
		for _, a := range next {
			for _, d := range body.Disabled {
				if a["name"] == d {
					a["enabled"] = false
				}
			}
		}
		s.apps = next
		_, _ = w.Write([]byte(`{}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *clockStub) puts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.orderPuts...)
}

func pointAtClock(a *App, url string) {
	off := false
	a.updateConfig(func(c *Config) {
		c.AWTRIX.HTTPBaseURL = url
		c.AWTRIX.AutoRediscover = &off
	})
}

func newClockApp(t *testing.T) (*App, *httptest.Server, *clockStub) {
	t.Helper()
	t.Setenv("EMBER_CLOCK", "")
	app, srv := newDevicesApp(t, "")
	stub := newClockStub(t)
	pointAtClock(app, stub.URL)
	return app, srv, stub
}

func probeClock(t *testing.T, a *App) {
	t.Helper()
	if dev := a.probeClockHealthWithin(context.Background(), time.Now(), 0); dev == nil || !dev.Reachable {
		t.Fatalf("probe failed: %+v", dev)
	}
}

func listDevices(t *testing.T, srv *httptest.Server) []deviceView {
	t.Helper()
	resp, b := devReq(t, srv, "GET", "/v1/devices", testToken, "")
	mustOK(t, "list", resp, b)
	var out struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Devices
}

func registeredClock(t *testing.T, a *App, srv *httptest.Server) deviceView {
	t.Helper()
	probeClock(t, a)
	for _, d := range listDevices(t, srv) {
		if d.Kind == deviceKindClock {
			return d
		}
	}
	t.Fatal("no clock record")
	return deviceView{}
}

func TestClockRecordCreatedOnFirstGoodProbe(t *testing.T) {
	a, srv, _ := newClockApp(t)
	epoch := a.devices.epochValue()
	d := registeredClock(t, a, srv)
	if d.ID != "clock-05ffb8" || d.HwID != testClockUID || d.Name != "Clock 05FFB8" {
		t.Fatalf("record = %+v", d)
	}
	if d.LastCheckin != nil || d.RotationPending {
		t.Fatalf("clock record has knob state: %+v", d)
	}
	s := d.LastSeen
	if s == nil || s.FW != "1.0.13" || s.IP != "192.0.2.66" || s.RSSI == nil || *s.RSSI != -61 || s.UptimeS == nil || *s.UptimeS != 42 {
		t.Fatalf("last_seen = %+v", s)
	}
	if d.ConfigVersion != a.clockConfigVersion() {
		t.Fatalf("config_version = %d, want %d", d.ConfigVersion, a.clockConfigVersion())
	}
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
	probeClock(t, a)
	if n := len(listDevices(t, srv)); n != 1 {
		t.Fatalf("second probe made %d records", n)
	}
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("second probe moved the epoch to %d", got)
	}
}

func TestClockRecordPersistsWithoutConfig(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	blob, _, err := a.store.GetSetting(devicesKey)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Devices []map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal([]byte(blob), &st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Devices[0]["config"]; ok {
		t.Fatalf("clock record stored a knob config: %s", blob)
	}
	if _, ok := st.Devices[0]["last_seen"]; !ok {
		t.Fatalf("clock record stored no last_seen: %s", blob)
	}
	reg := newDeviceRegistry(a.devices.kv)
	if err := reg.load(); err != nil {
		t.Fatal(err)
	}
	if v := reg.list(); len(v) != 1 || v[0].Kind != deviceKindClock || v[0].LastSeen == nil {
		t.Fatalf("reloaded = %+v", v)
	}
}

func TestClockRecordNotCreatedForOtherBoards(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.board = "awtrix3"
	probeClock(t, a)
	if n := len(listDevices(t, srv)); n != 0 {
		t.Fatalf("records = %d, want 0", n)
	}
}

func TestClockRecordCreatedOnDiscoverySwap(t *testing.T) {
	a, srv, stub := newClockApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: stub.URL, UID: testClockUID}}, nil
	}
	if !a.rediscoverClock(context.Background()) {
		t.Fatal("no swap")
	}
	devs := listDevices(t, srv)
	if len(devs) != 1 || devs[0].ID != "clock-05ffb8" || devs[0].LastSeen != nil {
		t.Fatalf("records = %+v", devs)
	}
}

func setStubUID(stub *clockStub, uid string) {
	stub.mu.Lock()
	stub.uid = uid
	stub.mu.Unlock()
}

func TestFlappingClockUIDNeitherFlipsNorBumps(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	epoch := a.devices.epochValue()
	for i := range 50 {
		setStubUID(stub, "spoof"+strconv.Itoa(i))
		probeClock(t, a)
	}
	for range 10 {
		setStubUID(stub, "aabbcc112233")
		probeClock(t, a)
		setStubUID(stub, testClockUID)
		probeClock(t, a)
	}
	devs := listDevices(t, srv)
	if len(devs) != 1 || devs[0].ID != d.ID || devs[0].HwID != testClockUID {
		t.Fatalf("records = %+v", devs)
	}
	if got := a.devices.epochValue(); got != epoch {
		t.Fatalf("epoch moved %d times", got-epoch)
	}
}

func TestLastSeenUpdateLeavesEpochAlone(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	epoch := a.devices.epochValue()
	for range 5 {
		probeClock(t, a)
	}
	if got := a.devices.epochValue(); got != epoch {
		t.Fatalf("last_seen updates moved the epoch to %d", got)
	}
}

func TestClockUIDReplacedAfterTwoProbes(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	epoch := a.devices.epochValue()
	setStubUID(stub, "aabbcc112233")
	probeClock(t, a)
	if devs := listDevices(t, srv); devs[0].ID != d.ID {
		t.Fatalf("one sighting replaced the record: %+v", devs)
	}
	probeClock(t, a)
	devs := listDevices(t, srv)
	if len(devs) != 1 || devs[0].ID != "clock-112233" || devs[0].HwID != "aabbcc112233" || devs[0].Name != "Clock 112233" {
		t.Fatalf("records = %+v", devs)
	}
	if !devs[0].CreatedAt.Equal(d.CreatedAt) {
		t.Fatalf("created_at moved: %v -> %v", d.CreatedAt, devs[0].CreatedAt)
	}
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
}

func TestClockUIDReplacementKeepsName(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+d.ID, testToken, `{"name":"Desk clock"}`)
	mustOK(t, "rename", resp, b)
	setStubUID(stub, "aabbcc112233")
	probeClock(t, a)
	probeClock(t, a)
	devs := listDevices(t, srv)
	if len(devs) != 1 || devs[0].ID != "clock-112233" || devs[0].Name != "Desk clock" {
		t.Fatalf("records = %+v", devs)
	}
}

func TestStaleProbeAfterRediscoveryNotRegistered(t *testing.T) {
	a, srv, stubA := newClockApp(t)
	d := registeredClock(t, a, srv)
	stubB := newClockStub(t)
	setStubUID(stubB, "bbbbbb000001")
	setStubUID(stubA, "aaaaaa000002")
	stubA.mu.Lock()
	stubA.onDevice = func() { pointAtClock(a, stubB.URL) }
	stubA.mu.Unlock()
	for range 2 {
		a.probeClockHealthWithin(context.Background(), time.Now(), 0)
		pointAtClock(a, stubA.URL)
	}
	devs := listDevices(t, srv)
	if len(devs) != 1 || devs[0].ID != d.ID || devs[0].HwID != testClockUID {
		t.Fatalf("a stale probe changed the record: %+v", devs)
	}
}

func TestClockUIDNormalised(t *testing.T) {
	for raw, want := range map[string]string{"E8:68:E7:05:FF:B8": testClockUID, " awtrix_test ": "awtrix_test"} {
		if got, ok := normalizeClockUID(raw); !ok || got != want {
			t.Errorf("normalizeClockUID(%q) = %q, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "a/b", strings.Repeat("a", 33)} {
		if _, ok := normalizeClockUID(raw); ok {
			t.Errorf("normalizeClockUID(%q) accepted", raw)
		}
	}
}

func TestClockRecordRenameWorks(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+d.ID, testToken, `{"name":"Desk clock"}`)
	mustOK(t, "rename", resp, b)
	probeClock(t, a)
	if got := listDevices(t, srv)[0].Name; got != "Desk clock" {
		t.Fatalf("name = %q", got)
	}
}

func TestClockKindGatesOnKnobRoutes(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1/devices/" + d.ID + "/rotate", "", http.StatusBadRequest},
		{"GET", "/v1/devices/" + d.ID + "/stats", "", http.StatusNotFound},
		{"GET", "/v1/devices/" + d.ID + "/ota", "", http.StatusNotFound},
		{"PUT", "/v1/devices/" + d.ID + "/ota", `{"mode":"auto"}`, http.StatusNotFound},
		{"GET", "/v1/devices/" + d.ID + "/coredumps", "", http.StatusNotFound},
		{"POST", "/v1/devices", `{"kind":"awtrix-ng","hw_id":"` + testClockUID + `"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		resp, b := devReq(t, srv, c.method, c.path, testToken, c.body)
		if resp.StatusCode != c.want {
			t.Errorf("%s %s = %d, want %d: %s", c.method, c.path, resp.StatusCode, c.want, b)
		}
	}
	if _, err := a.devices.checkin(d.ID, deviceCheckin{}, nil, ""); err == nil || !strings.Contains(err.Error(), "checkin applies to kind") {
		t.Fatalf("checkin err = %v", err)
	}
}

func TestClockRecordNeverAuthenticates(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	token := newToken(deviceTokenPrefix)
	a.devices.mu.Lock()
	rec := a.devices.state.find(d.ID)
	rec.TokenSHA256 = tokenHash(token)
	rec.PendingTokenSHA256 = tokenHash(token)
	a.devices.mu.Unlock()
	if _, ok, err := a.devices.authenticate(token); ok || err != nil {
		t.Fatalf("clock authenticated: ok=%v err=%v", ok, err)
	}
	for _, p := range []string{"/v1/devices/self/config", "/v1/devices/self/view"} {
		if resp, b := devReq(t, srv, "GET", p, token, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d: %s", p, resp.StatusCode, b)
		}
	}
}

func TestOTASkipsClockRecords(t *testing.T) {
	r := newDeviceRegistry(func() settingsKV { return nil })
	if _, _, err := r.seenClock(testClockUID, nil, 1); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	d := &r.state.Devices[0]
	d.OTA = &knobOTA{Target: "0.9.0", Version: "0.9.0", Phase: otaPhaseDownloading}
	d.LastCheckin = &deviceCheckin{FW: "0.9.0"}
	r.mu.Unlock()
	if r.otaTargets("0.9.0") {
		t.Error("otaTargets counted a clock record")
	}
	if r.otaKeeps("0.9.0") {
		t.Error("otaKeeps counted a clock record")
	}
	d.OTA.Phase = otaPhaseFailed
	d.OTA.Blocked = []string{"0.9.0"}
	epoch := r.epochValue()
	if err := r.otaRetire([]string{"0.9.0"}, ""); err != nil {
		t.Fatal(err)
	}
	if r.epochValue() != epoch || len(r.state.Devices[0].OTA.Blocked) != 1 {
		t.Error("otaRetire touched a clock record")
	}
}

func TestClockRecordDeleteRecreatedByNextProbe(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	resp, b := devReq(t, srv, "PATCH", "/v1/devices/"+d.ID, testToken, `{"name":"Old name"}`)
	mustOK(t, "rename", resp, b)
	resp, b = devReq(t, srv, "DELETE", "/v1/devices/"+d.ID, testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	if n := len(listDevices(t, srv)); n != 0 {
		t.Fatalf("records after delete = %d", n)
	}
	again := registeredClock(t, a, srv)
	if again.ID != d.ID || again.Name != "Clock 05FFB8" {
		t.Fatalf("recreated = %+v", again)
	}
}

func TestDoctorDevicesListsClockRecord(t *testing.T) {
	a, srv, _ := newClockApp(t)
	mintKnob(t, srv, http.StatusCreated)
	d := registeredClock(t, a, srv)
	res := checkDevices(a)
	if !strings.Contains(res.Detail, "registered=2") || !strings.Contains(res.Detail, d.ID+" seen=") {
		t.Fatalf("detail = %q", res.Detail)
	}
	if strings.Contains(res.Detail, d.ID+" never checked in") {
		t.Fatalf("clock judged by checkin: %q", res.Detail)
	}
	now := time.Now()
	a.devices.now = func() time.Time { return now.Add(deviceStaleAfter + time.Minute) }
	res = checkDevices(a)
	if res.Status != StatusWarn || !strings.Contains(res.Detail, d.ID+" seen=") {
		t.Fatalf("stale clock: %+v", res)
	}
}

func TestDoctorDevicesClockDisabled(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	t.Setenv("EMBER_CLOCK", "off")
	res := checkDevices(a)
	if res.Status != StatusOK || !strings.Contains(res.Detail, d.ID+" clock disabled") {
		t.Fatalf("res = %+v", res)
	}
}

func TestClockConfigChangeBumpsEpoch(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	epoch := a.devices.epochValue()
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"moon_phase":false}`)
	mustOK(t, "weather put", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch after old-endpoint change = %d, want %d", got, epoch+1)
	}
	after := listDevices(t, srv)[0]
	if after.ConfigVersion == d.ConfigVersion || after.ConfigVersion != a.clockConfigVersion() {
		t.Fatalf("config_version %d -> %d (want %d)", d.ConfigVersion, after.ConfigVersion, a.clockConfigVersion())
	}
	resp, _ = devReq(t, srv, "GET", "/state", "", "")
	if got := resp.Header.Get(devicesEpochHeader); got != strconv.FormatUint(epoch+1, 10) {
		t.Fatalf("/state epoch header = %q", got)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/apps", testToken, `{"app":"codex","enabled":false}`)
	mustOK(t, "apps put", resp, b)
	if got := a.devices.epochValue(); got != epoch+2 {
		t.Fatalf("epoch after hidden app = %d, want %d", got, epoch+2)
	}
	resp, b = devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"refresh_minutes":30}`)
	mustOK(t, "weather source put", resp, b)
	if got := a.devices.epochValue(); got != epoch+2 {
		t.Fatalf("a source-only change moved the epoch to %d", got)
	}
}

func TestReapplySettingsBumpsEpochAtMostOnce(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	for path, body := range map[string]string{
		"/v1/weather/config":  `{"moon_phase":false,"forecast_tile":false}`,
		"/v1/meetings/config": `{"tile_lead_minutes":12}`,
		"/v1/usage/config":    `{"usage_widget":false}`,
	} {
		resp, b := devReq(t, srv, "PUT", path, testToken, body)
		mustOK(t, path, resp, b)
	}
	stored := a.clockConfigVersion()
	a.updateConfig(func(c *Config) {
		c.Weather = defaultConfig().Weather
		c.Weather.applyDefaults()
	})
	epoch := a.devices.epochValue()
	a.reapplySettings()
	if got := a.devices.epochValue(); got > epoch+1 {
		t.Fatalf("reapply moved the epoch %d times", got-epoch)
	}
	if got := listDevices(t, srv)[0].ConfigVersion; got != stored {
		t.Fatalf("config_version after reapply = %d, want %d", got, stored)
	}
}
