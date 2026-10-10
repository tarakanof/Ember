package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const customClockConfig = `{
	"apps": {
		"agents": {"usage_cards": false, "usage_per_model": false, "hidden_tools": ["t3", "codex"]},
		"focus": {"focus_color": "#112233", "break_color": "#445566"},
		"weather": {"on": false, "native_icon": true, "forecast": false, "forecast_hours": 6, "air": false,
			"moon": false, "overlay": false,
			"popups": {"on_change": false, "sun": false, "severe": false, "native_icons": true,
				"interval_minutes": 0, "duration_seconds": 8},
			"icon_ids": {"clear": "123"}},
		"calendar": {"on": false, "tile_lead_minutes": 15, "popup_lead_minutes": 0}
	},
	"rotation": {"order": ["date", "time"], "disabled": ["hum"]}
}`

func clockConfigReq(t *testing.T, srv *httptest.Server, method, id, body string) (*http.Response, []byte) {
	t.Helper()
	return devReq(t, srv, method, "/v1/devices/"+id+"/config", testToken, body)
}

func getClockConfig(t *testing.T, srv *httptest.Server, id string) (clockConfig, int) {
	t.Helper()
	resp, b := clockConfigReq(t, srv, "GET", id, "")
	mustOK(t, "clock config get", resp, b)
	var c clockConfig
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	v, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion))
	return c, v
}

func TestClockConfigGolden(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	get := func() []byte {
		t.Helper()
		resp, b := clockConfigReq(t, srv, "GET", d.ID, "")
		return mustOK(t, "clock config", resp, b)
	}
	assertClockGolden(t, "config_default", get())
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, customClockConfig)
	mustOK(t, "clock config put", resp, b)
	assertClockGolden(t, "config_custom", get())
}

func assertClockGolden(t *testing.T, name string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", name, err, body)
	}
	buf.WriteByte('\n')
	compareGolden(t, filepath.Join("testdata", "clock", name+".json"), buf.Bytes())
}

func TestClockConfigVersionIsRecordCounter(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	_, v := getClockConfig(t, srv, d.ID)
	if v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("header %d, record %d", v, listDevices(t, srv)[0].ConfigVersion)
	}
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}}}`)
	mustOK(t, "put", resp, b)
	if got, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion)); got != v+1 {
		t.Fatalf("version after one change = %d, want %d", got, v+1)
	}
	resp, b = clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":true}}}`)
	mustOK(t, "put back", resp, b)
	if got, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion)); got != v+2 {
		t.Fatalf("version after changing back = %d, want %d (a counter, not a hash)", got, v+2)
	}
}

func TestClockConfigFacadeMatchesOldEndpoints(t *testing.T) {
	facade, fsrv, fstub := newClockApp(t)
	fd := registeredClock(t, facade, fsrv)
	legacy, lsrv, lstub := newClockApp(t)
	registeredClock(t, legacy, lsrv)

	resp, b := clockConfigReq(t, fsrv, "PUT", fd.ID, `{
		"apps": {
			"agents": {"usage_cards": false, "hidden_tools": ["codex"]},
			"focus": {"focus_color": "#112233"},
			"weather": {"forecast": false, "air": false, "popups": {"interval_minutes": 0, "sun": false}, "icon_ids": {"rain": "77"}},
			"calendar": {"tile_lead_minutes": 15, "popup_lead_minutes": 0}
		},
		"rotation": {"order": ["date", "time"], "disabled": ["hum"]}
	}`)
	mustOK(t, "facade put", resp, b)

	for _, c := range []struct{ path, body string }{
		{"/v1/usage/config", `{"usage_widget":false}`},
		{"/v1/apps", `{"app":"codex","enabled":false}`},
		{"/v1/pomodoro/config", `{"focus_color":"#112233"}`},
		{"/v1/weather/config", `{"forecast_tile":false,"air_tile":false,"popup_interval_minutes":0,"sun_popups":false,"icon_ids":{"rain":"77"}}`},
		{"/v1/meetings/config", `{"tile_lead_minutes":15,"popup_lead_minutes":0}`},
		{"/v1/device/apps", `{"order":["date","time"],"disabled":["hum"]}`},
	} {
		resp, b := devReq(t, lsrv, "PUT", c.path, testToken, c.body)
		mustOK(t, c.path, resp, b)
	}

	for _, key := range []string{clockConfigKey, pomodoroSettingsKey, weatherSettingsKey, meetingsSettingsKey, usageSettingsKey, hiddenAppsKey} {
		fv, fok, _ := facade.store.GetSetting(key)
		lv, lok, _ := legacy.store.GetSetting(key)
		if fv != lv || fok != lok {
			t.Errorf("%s differs:\nfacade %s\nlegacy %s", key, fv, lv)
		}
	}
	fcfg, _ := json.Marshal(publicConfig(*facade.cfg.Load()))
	lcfg, _ := json.Marshal(publicConfig(*legacy.cfg.Load()))
	if string(fcfg) != string(lcfg) {
		t.Errorf("effective config differs:\nfacade %s\nlegacy %s", fcfg, lcfg)
	}
	if !reflect.DeepEqual(fstub.puts(), lstub.puts()) {
		t.Errorf("clock writes differ:\nfacade %v\nlegacy %v", fstub.puts(), lstub.puts())
	}
	fc, _ := getClockConfig(t, fsrv, fd.ID)
	lc, _ := getClockConfig(t, lsrv, fd.ID)
	if !reflect.DeepEqual(fc, lc) {
		t.Errorf("composed config differs:\nfacade %+v\nlegacy %+v", fc, lc)
	}
}

func publicConfig(c Config) Config {
	c.AWTRIX = AWTRIXConfig{}
	return c
}

func TestClockConfigMergeRules(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	put := func(body string) clockConfig {
		t.Helper()
		resp, b := clockConfigReq(t, srv, "PUT", d.ID, body)
		mustOK(t, "put "+body, resp, b)
		var c clockConfig
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	before, _ := getClockConfig(t, srv, d.ID)
	c := put(`{"apps":{"weather":{"popups":{"sun":false}}}}`)
	want := before.Apps.Weather.Popups
	want.Sun = false
	if c.Apps.Weather.Popups != want || c.Apps.Weather.Forecast != before.Apps.Weather.Forecast {
		t.Fatalf("nested merge: %+v, want %+v", c.Apps.Weather.Popups, want)
	}
	put(`{"apps":{"agents":{"hidden_tools":["b","a","a"]}}}`)
	if c := put(`{"apps":{"agents":{"hidden_tools":["c"]}}}`); !reflect.DeepEqual(c.Apps.Agents.HiddenTools, []string{"c"}) {
		t.Fatalf("hidden_tools = %v, want [c]", c.Apps.Agents.HiddenTools)
	}
	if c := put(`{"apps":{"agents":{"hidden_tools":["b","a","a"]}}}`); !reflect.DeepEqual(c.Apps.Agents.HiddenTools, []string{"a", "b"}) {
		t.Fatalf("hidden_tools = %v, want [a b]", c.Apps.Agents.HiddenTools)
	}
	put(`{"apps":{"weather":{"icon_ids":{"clear":"1"}}}}`)
	if c := put(`{"apps":{"weather":{"icon_ids":{"rain":"2"}}}}`); !reflect.DeepEqual(c.Apps.Weather.IconIDs, map[string]string{"rain": "2"}) {
		t.Fatalf("icon_ids = %v, want rain only", c.Apps.Weather.IconIDs)
	}
}

func TestClockConfigEmptyPatchIsNoOp(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	_, v := getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	weather, _, _ := a.store.GetSetting(weatherSettingsKey)
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{}`)
	mustOK(t, "empty put", resp, b)
	if got, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion)); got != v {
		t.Fatalf("version %d -> %d", v, got)
	}
	if a.devices.epochValue() != epoch {
		t.Fatal("empty put moved the epoch")
	}
	if after, _, _ := a.store.GetSetting(weatherSettingsKey); after != weather {
		t.Fatal("empty put wrote the weather slice")
	}
	if len(stub.puts()) != 0 {
		t.Fatal("empty put wrote to the clock")
	}
}

func TestClockConfigRejectsInvalidBodies(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	before, v := getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	for _, body := range []string{
		`[]`,
		`"x"`,
		`{"bogus":1}`,
		`{"schema":2}`,
		`{"apps":{"music":{}}}`,
		`{"apps":{"weather":{"bogus":true}}}`,
		`{"apps":{"weather":{"popups":{"loud":true}}}}`,
		`{"apps":{"focus":{"on":true}}}`,
		`{"apps":{"focus":{"focus_color":"red"}}}`,
		`{"apps":{"weather":{"forecast_hours":48}}}`,
		`{"apps":{"weather":{"icon_ids":{"clear":"abc"}}}}`,
		`{"apps":{"calendar":{"tile_lead_minutes":0}}}`,
		`{"apps":{"agents":{"hidden_tools":[""]}}}`,
		`{"apps":{"agents":{"usage_cards":"yes"}}}`,
		`{"apps":{"focus":{"focus_color":"red"}},"rotation":{"order":["time"]}}`,
		`{"rotation":{"order":["time"],"speed":3}}`,
	} {
		resp, b := clockConfigReq(t, srv, "PUT", d.ID, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400: %s", body, resp.StatusCode, b)
		}
	}
	after, v2 := getClockConfig(t, srv, d.ID)
	if !reflect.DeepEqual(before, after) || v != v2 || a.devices.epochValue() != epoch {
		t.Fatalf("a rejected PUT changed state")
	}
	if len(stub.puts()) != 0 {
		t.Fatalf("a rejected PUT wrote to the clock: %v", stub.puts())
	}
}

func TestClockConfigRotationWriteFailureChangesNothing(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	before, _ := getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.orderFail = true
	stub.mu.Unlock()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date"]}}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("PUT = %d, want 502: %s", resp.StatusCode, b)
	}
	after, _ := getClockConfig(t, srv, d.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed clock write changed the config:\n%+v\n%+v", before, after)
	}
}

func TestClockConfigPutBumpsEpochOnce(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false},"agents":{"hidden_tools":["codex"]},"focus":{"break_color":"#010203"}}}`)
	mustOK(t, "put", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
	v, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion))
	if v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("header version %d, record %d", v, listDevices(t, srv)[0].ConfigVersion)
	}
}

func TestClockConfigPutWithRotationBumpsEpochOnce(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	mustOK(t, "put", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
}

func TestClockConfigOldAppOrderMovesVersionOnce(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	_, v := getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 || listDevices(t, srv)[0].ConfigVersion == v {
		t.Fatalf("epoch = %d after the write, want %d with a new version", got, epoch+1)
	}
	resp, b = devReq(t, srv, "GET", "/v1/device/apps", testToken, "")
	mustOK(t, "apps read", resp, b)
	c, v2 := getClockConfig(t, srv, d.ID)
	if c.Rotation == nil || c.Rotation.Order[0] != "date" || a.devices.epochValue() != epoch+1 {
		t.Fatalf("rotation = %+v epoch %d", c.Rotation, a.devices.epochValue())
	}
	if v2 != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("header version %d, record %d", v2, listDevices(t, srv)[0].ConfigVersion)
	}
}

func TestClockConfigOldAppOrderUnchangedDoesNotMoveVersion(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	_, v := getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["time","date"],"disabled":["hum"]}`)
	mustOK(t, "apps order", resp, b)
	a.syncClockConfigVersion()
	if _, v2 := getClockConfig(t, srv, d.ID); v2 != v || a.devices.epochValue() != epoch {
		t.Fatalf("an unchanged order moved the version: epoch %d, want %d", a.devices.epochValue(), epoch)
	}
}

func TestClockConfigOldAppOrderReadbackFailureKeepsVersionConsistent(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	stub.mu.Lock()
	stub.appsFail = true
	stub.mu.Unlock()
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	c, v := getClockConfig(t, srv, d.ID)
	if c.Rotation != nil {
		t.Fatalf("rotation = %+v, want null while the list can't be read", c.Rotation)
	}
	if rec := listDevices(t, srv)[0].ConfigVersion; v != rec || a.devices.epochValue() != epoch+1 {
		t.Fatalf("header version %d, record %d, epoch %d (want %d)", v, rec, a.devices.epochValue(), epoch+1)
	}
	stub.mu.Lock()
	stub.appsFail = false
	stub.mu.Unlock()
	c, v = getClockConfig(t, srv, d.ID)
	if c.Rotation == nil || c.Rotation.Order[0] != "date" || v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("rotation = %+v after the list came back, version %d", c.Rotation, v)
	}
}

func TestClockConfigPushedAppsDoNotMoveVersion(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	_, v := getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	stub.mu.Lock()
	stub.apps = append(stub.apps, map[string]any{"name": "ember-weather", "enabled": true, "inLoop": true, "origin": "pushed"})
	stub.mu.Unlock()
	if _, v2 := getClockConfig(t, srv, d.ID); v2 != v || a.devices.epochValue() != epoch {
		t.Fatal("a pushed Ember tile moved the clock config version")
	}
}

func TestClockConfigRotationRoundTripsWithoutClockWrite(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	c, _ := getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.orderFail = true
	stub.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"apps": map[string]any{"weather": map[string]any{"moon": false}}, "rotation": c.Rotation})
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, string(body))
	mustOK(t, "resend rotation", resp, b)
	if n := len(stub.puts()); n != 0 {
		t.Fatalf("an unchanged rotation was written to the clock %d times", n)
	}
}

func TestClockConfigRotationReadbackFailureReturnsNull(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsFail = true
	stub.mu.Unlock()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"rotation":{"order":["date","time"]}}`)
	mustOK(t, "put", resp, b)
	var c clockConfig
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Rotation != nil {
		t.Fatalf("rotation = %+v, want null after a failed readback", c.Rotation)
	}
	v, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion))
	if v != a.clockConfigVersion() || a.clockConfigDigestStored() != clockConfigDigest(c) {
		t.Fatalf("version %d does not describe the body", v)
	}
}

func holdClockOrder(t *testing.T, stub *clockStub) (release func()) {
	t.Helper()
	hold, in := make(chan struct{}), make(chan struct{}, 1)
	stub.mu.Lock()
	stub.orderHold, stub.orderIn = hold, in
	stub.mu.Unlock()
	release = sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	return release
}

func TestClockConfigPutKeepsConcurrentOldEndpointWrite(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	put := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"calendar":{"tile_lead_minutes":15}},"rotation":{"order":["date","time"]}}`)
	waitEntered(t, stub.orderIn, "facade order write")
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"moon_phase":false}`)
	mustOK(t, "weather put", resp, b)
	resp, b = devReq(t, srv, "PUT", "/v1/apps", testToken, `{"app":"codex","enabled":false}`)
	mustOK(t, "apps put", resp, b)
	release()
	mustReply(t, "facade put", put)
	c, _ := getClockConfig(t, srv, d.ID)
	if c.Apps.Weather.Moon || c.Apps.Calendar.TileLeadMinutes != 15 || !slices.Equal(c.Apps.Agents.HiddenTools, []string{"codex"}) {
		t.Fatalf("lost update: %+v", c.Apps)
	}
	blob, _, _ := a.store.GetSetting(clockConfigKey)
	if !strings.Contains(blob, `"moon":false`) {
		t.Fatalf("stored clock config lost moon: %s", blob)
	}
}

func TestClockConfigConcurrentFacadePutsRunOneAtATime(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	first := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"calendar":{"tile_lead_minutes":15}},"rotation":{"order":["date","time"]}}`)
	waitEntered(t, stub.orderIn, "first facade order write")
	second := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"weather":{"moon":false},"agents":{"usage_cards":false}}}`)
	if r, done := waitReply(second, 200*time.Millisecond); done {
		t.Fatalf("a second facade PUT finished (%d) while the first waited on the clock", r.code)
	}
	release()
	mustReply(t, "first put", first)
	r := mustReply(t, "second put", second)
	if rec := listDevices(t, srv)[0].ConfigVersion; r.version != rec {
		t.Fatalf("second put header version %d, record %d", r.version, rec)
	}
	c, _ := getClockConfig(t, srv, d.ID)
	if c.Apps.Weather.Moon || c.Apps.Agents.UsageCards || c.Apps.Calendar.TileLeadMinutes != 15 {
		t.Fatalf("lost update: %+v", c.Apps)
	}
}

func TestClockConfigRotationNullWhenClockOff(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	t.Setenv("EMBER_CLOCK", "off")
	c, _ := getClockConfig(t, srv, d.ID)
	if c.Rotation != nil {
		t.Fatalf("rotation = %+v, want null before any read", c.Rotation)
	}
}

func TestKnobConfigUnaffectedByClockRecord(t *testing.T) {
	a, srv, _ := newClockApp(t)
	m := mintKnob(t, srv, http.StatusCreated)
	registeredClock(t, a, srv)
	resp, b := devReq(t, srv, "PUT", "/v1/devices/"+m.ID+"/config", testToken, `{"apps":{}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("knob PUT with a clock key = %d: %s", resp.StatusCode, b)
	}
	resp, b = devReq(t, srv, "GET", "/v1/devices/"+m.ID+"/config", testToken, "")
	mustOK(t, "knob config", resp, b)
	var k knobSettings
	if err := json.Unmarshal(b, &k); err != nil || len(k.Pages) == 0 {
		t.Fatalf("knob config = %s", b)
	}
}

func TestClockConfigPersistFailureChangesNothing(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "")
	dbPath := filepath.Join(t.TempDir(), "s.db")
	a, srv := newDevicesApp(t, dbPath)
	stub := newClockStub(t)
	pointAtClock(a, stub.URL)
	d := registeredClock(t, a, srv)
	before, _ := getClockConfig(t, srv, d.ID)
	usage, _, _ := a.store.GetSetting(usageSettingsKey)
	row, _, _ := a.store.GetSetting(clockConfigKey)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, op := range []string{"INSERT", "UPDATE"} {
		if _, err := db.Exec(`CREATE TRIGGER fail_clock_` + op + ` BEFORE ` + op + ` ON settings WHEN NEW.key = 'clock_config_json' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
			t.Fatal(err)
		}
	}
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"agents":{"usage_cards":false,"hidden_tools":["codex"]},"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(b), "app order was already written") {
		t.Fatalf("PUT = %d: %s", resp.StatusCode, b)
	}
	after, _ := getClockConfig(t, srv, d.ID)
	if !reflect.DeepEqual(before.Apps, after.Apps) {
		t.Fatalf("memory changed after a failed store:\n%+v\n%+v", before.Apps, after.Apps)
	}
	if got, _, _ := a.store.GetSetting(usageSettingsKey); got != usage {
		t.Fatalf("usage_json written in a failed batch: %s", got)
	}
	if got, _, _ := a.store.GetSetting(clockConfigKey); got != row {
		t.Fatalf("clock_config_json written in a failed batch: %s", got)
	}
	if _, ok, _ := a.store.GetSetting(hiddenAppsKey); ok {
		t.Fatal("display_hidden_apps written in a failed batch")
	}
}

func TestClockConfigHiddenToolsFromOldEndpointRoundTrip(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	long := strings.Repeat("x", 100)
	for i := range 70 {
		resp, b := devReq(t, srv, "PUT", "/v1/apps", testToken, `{"app":"`+long+strconv.Itoa(i)+`","enabled":false}`)
		mustOK(t, "apps put", resp, b)
	}
	resp, b := clockConfigReq(t, srv, "GET", d.ID, "")
	mustOK(t, "get", resp, b)
	resp, b = clockConfigReq(t, srv, "PUT", d.ID, string(b))
	mustOK(t, "put back", resp, b)
}

func TestClockConfigRotationResendAfterOutsideReorderIsWritten(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "old reorder", resp, b)
	resp, b = clockConfigReq(t, srv, "PUT", d.ID, `{"rotation":{"order":["time","date"],"disabled":["hum"]}}`)
	mustOK(t, "facade put", resp, b)
	if n := len(stub.puts()); n != 2 {
		t.Fatalf("clock order writes = %d, want 2", n)
	}
	c, _ := getClockConfig(t, srv, d.ID)
	if c.Rotation == nil || c.Rotation.Order[0] != "time" {
		t.Fatalf("clock rotation = %+v, want time first", c.Rotation)
	}
}

func TestClockConfigRotationWrittenWhenReadFails(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	c, _ := getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsFail = true
	stub.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"rotation": c.Rotation})
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, string(body))
	mustOK(t, "put", resp, b)
	if n := len(stub.puts()); n != 1 {
		t.Fatalf("clock order writes = %d, want 1 when the list could not be read", n)
	}
}

func TestClockConfigRotationResendAfterWebUIReorderIsWritten(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	c, _ := getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.apps[0], stub.apps[1] = stub.apps[1], stub.apps[0]
	stub.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"rotation": c.Rotation})
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, string(body))
	mustOK(t, "resend rotation", resp, b)
	if n := len(stub.puts()); n != 1 {
		t.Fatalf("clock order writes = %d, want 1 after a reorder on the clock", n)
	}
	after, _ := getClockConfig(t, srv, d.ID)
	if !reflect.DeepEqual(after.Rotation, c.Rotation) {
		t.Fatalf("rotation = %+v, want %+v", after.Rotation, c.Rotation)
	}
}

func assertClockRecordInStep(t *testing.T, a *App, srv *httptest.Server) {
	t.Helper()
	if rec, want := listDevices(t, srv)[0].ConfigVersion, a.clockConfigVersion(); rec != want {
		t.Fatalf("record version %d, composed config hashes to %d", rec, want)
	}
}

type reqReply struct {
	code    int
	version int
	body    []byte
	err     error
}

func clockGoReq(srv *httptest.Server, method, path, body string) <-chan reqReply {
	ch := make(chan reqReply, 1)
	go func() {
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			ch <- reqReply{err: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			ch <- reqReply{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		v, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion))
		ch <- reqReply{code: resp.StatusCode, version: v, body: b, err: err}
	}()
	return ch
}

func waitReply(ch <-chan reqReply, d time.Duration) (reqReply, bool) {
	select {
	case r := <-ch:
		return r, true
	case <-time.After(d):
		return reqReply{}, false
	}
}

func mustReply(t *testing.T, what string, ch <-chan reqReply) reqReply {
	t.Helper()
	r, ok := waitReply(ch, 15*time.Second)
	if !ok {
		t.Fatalf("%s: no reply", what)
	}
	if r.err != nil || r.code != http.StatusOK {
		t.Fatalf("%s: %d %v %s", what, r.code, r.err, r.body)
	}
	return r
}

func TestClockRotationReadInFlightDelaysOldRouteWrite(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	in, release := stub.holdApps(t, http.StatusOK)
	get := clockGoReq(srv, "GET", "/v1/device/apps", "")
	waitEntered(t, in, "apps read")
	put := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["date","time"]}`)
	if r, done := waitReply(put, 200*time.Millisecond); done {
		t.Fatalf("old-route PUT finished (%d) while a read of the list was in flight", r.code)
	}
	release()
	mustReply(t, "apps read", get)
	mustReply(t, "apps order", put)
	if r := a.clockRotation.Load(); r == nil || r.Order[0] != "date" || a.devices.epochValue() != epoch+1 {
		t.Fatalf("rotation = %+v epoch %d, want date first and epoch %d", r, a.devices.epochValue(), epoch+1)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockRotationOldRouteWritesRunOneAtATime(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	in, release := stub.holdApps(t, http.StatusInternalServerError)
	first := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["time","date"]}`)
	waitEntered(t, in, "first old-route read-back")
	second := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["date","time"]}`)
	if r, done := waitReply(second, 200*time.Millisecond); done {
		t.Fatalf("second old-route PUT finished (%d) during the first one's read-back", r.code)
	}
	release()
	mustReply(t, "first order", first)
	mustReply(t, "second order", second)
	if r := a.clockRotation.Load(); r == nil || r.Order[0] != "date" {
		t.Fatalf("rotation = %+v, want the second write's list", r)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockRotationDelayedWriteReplyKeepsNewerList(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.holdAfter = true
	stub.mu.Unlock()
	release := holdClockOrder(t, stub)
	first := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["date","time"]}`)
	waitEntered(t, stub.orderIn, "first order write")
	second := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["time","date"]}`)
	early, secondDone := waitReply(second, 300*time.Millisecond)
	stub.mu.Lock()
	stub.appsFailN = 1
	stub.mu.Unlock()
	release()
	mustReply(t, "first order", first)
	if !secondDone {
		early = mustReply(t, "second order", second)
	}
	if early.code != http.StatusOK {
		t.Fatalf("second order: %d %s", early.code, early.body)
	}
	if r := a.clockRotation.Load(); r == nil || r.Order[0] != "time" {
		t.Fatalf("rotation = %+v, want time first: the clock applied the second write last", r)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockConfigPutWithRotationBumpsOnceFromStaleCache(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.apps[0], stub.apps[1] = stub.apps[1], stub.apps[0]
	stub.mu.Unlock()
	epoch := a.devices.epochValue()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}},"rotation":{"order":["time","date"],"disabled":["hum"]}}`)
	mustOK(t, "put", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockConfigPutWithRotationBumpsOnceFromColdCache(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	a.clockRotation.Store(nil)
	a.syncClockConfigVersion()
	epoch := a.devices.epochValue()
	d := listDevices(t, srv)[0]
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}},"rotation":{"order":["time","date"],"disabled":["hum"]}}`)
	mustOK(t, "put", resp, b)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d", got, epoch+1)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockConfigGetWaitsForFacadeCommit(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	hold, in := make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	a.commitHook = func() {
		once.Do(func() {
			in <- struct{}{}
			<-hold
		})
	}
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	put := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	waitEntered(t, in, "facade commit")
	get := clockGoReq(srv, "GET", "/v1/devices/"+d.ID+"/config", "")
	if r, done := waitReply(get, 200*time.Millisecond); done {
		if rec := listDevices(t, srv)[0].ConfigVersion; r.version != rec {
			t.Fatalf("GET during a facade commit: header version %d, record %d", r.version, rec)
		}
		t.Fatal("GET finished during a facade commit")
	}
	release()
	mustReply(t, "facade put", put)
	r := mustReply(t, "facade get", get)
	var c clockConfig
	if err := json.Unmarshal(r.body, &c); err != nil {
		t.Fatal(err)
	}
	if c.Apps.Weather.Moon || c.Rotation == nil || c.Rotation.Order[0] != "date" || r.version != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("GET after the commit: %+v version %d", c, r.version)
	}
}

func TestClockConfigOldRouteWaitsForFacadePut(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	put := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	waitEntered(t, stub.orderIn, "facade order write")
	old := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["time","date"]}`)
	if r, done := waitReply(old, 200*time.Millisecond); done {
		t.Fatalf("old-route PUT finished (%d) while a facade PUT waited on the clock", r.code)
	}
	release()
	mustReply(t, "facade put", put)
	mustReply(t, "apps order", old)
	c, v := getClockConfig(t, srv, d.ID)
	if c.Rotation == nil || c.Rotation.Order[0] != "time" || v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("rotation = %+v version %d record %d", c.Rotation, v, listDevices(t, srv)[0].ConfigVersion)
	}
}

func TestClockConfigOldAppOrderMalformedReadbackClears(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsBody = `[{"name":`
	stub.mu.Unlock()
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	if r := a.clockRotation.Load(); r != nil {
		t.Fatalf("rotation = %+v, want null after a malformed read-back", r)
	}
	assertClockRecordInStep(t, a, srv)
	c, v := getClockConfig(t, srv, d.ID)
	if c.Rotation != nil || v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("rotation = %+v, header %d, record %d", c.Rotation, v, listDevices(t, srv)[0].ConfigVersion)
	}
}

func TestClockConfigRotationMalformedReadbackReturnsNull(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsBody = `[{"name":`
	stub.mu.Unlock()
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"rotation":{"order":["date","time"]}}`)
	mustOK(t, "put", resp, b)
	var c clockConfig
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Rotation != nil || len(stub.puts()) != 1 {
		t.Fatalf("rotation = %+v writes %d, want null and 1 write", c.Rotation, len(stub.puts()))
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockConfigOldAppOrderReadbackHasTwoSecondBudget(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsDelay = 10 * time.Second
	stub.mu.Unlock()
	start := time.Now()
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	elapsed := time.Since(start)
	mustOK(t, "apps order", resp, b)
	if elapsed < clockRotationReadBudget || elapsed > clockRotationReadBudget+900*time.Millisecond {
		t.Fatalf("old-route PUT took %v, want about %v", elapsed, clockRotationReadBudget)
	}
	if r := a.clockRotation.Load(); r != nil {
		t.Fatalf("rotation = %+v, want null after a timed-out read-back", r)
	}
	assertClockRecordInStep(t, a, srv)
}

func waitCode(t *testing.T, what string, ch <-chan reqReply) reqReply {
	t.Helper()
	r, ok := waitReply(ch, 15*time.Second)
	if !ok || r.err != nil {
		t.Fatalf("%s: no reply (%v)", what, r.err)
	}
	return r
}

func TestClockResyncCannotHashAHalfCommittedFacadePut(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	epoch := a.devices.epochValue()
	hold, in := make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	a.commitHook = func() {
		once.Do(func() {
			in <- struct{}{}
			<-hold
		})
	}
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	a.clockSync.mu.Lock()
	unlock := sync.OnceFunc(a.clockSync.mu.Unlock)
	t.Cleanup(unlock)
	stalled := make(chan struct{})
	go func() {
		defer close(stalled)
		a.syncClockConfigVersion()
	}()
	time.Sleep(50 * time.Millisecond)
	put := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	select {
	case <-in:
		unlock()
		recvWithin(t, stalled, "clock config resync")
	case <-time.After(300 * time.Millisecond):
		unlock()
		recvWithin(t, stalled, "clock config resync")
		waitEntered(t, in, "facade commit")
	}
	release()
	mustReply(t, "facade put", put)
	if got := a.devices.epochValue(); got != epoch+1 {
		t.Fatalf("epoch = %d, want %d: a stalled resync stored a half-committed version", got, epoch+1)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestAdminReloadWaitsForClockRotationBeforePublishing(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"old"}}`)
	if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"new"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app.rotationOp.slot <- struct{}{}
	release := sync.OnceFunc(func() { <-app.rotationOp.slot })
	t.Cleanup(release)
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		handleAdminReload(app)(w, httptest.NewRequest("POST", "/admin/reload", nil))
		done <- w.Code
	}()
	time.Sleep(200 * time.Millisecond)
	if got := app.cfg.Load().Display.IdleText; got != "old" {
		t.Fatalf("reload published idle_text %q while a clock app order operation held the lock", got)
	}
	release()
	if code := recvWithin(t, done, "admin reload"); code != http.StatusOK {
		t.Fatalf("reload = %d, want 200", code)
	}
	if got := app.cfg.Load().Display.IdleText; got != "new" {
		t.Fatalf("idle_text = %q after the reload, want new", got)
	}
}

func TestClockConfigPutOutOfTimeInsideCommitChangesNoSettings(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	before, _ := getClockConfig(t, srv, d.ID)
	a.rotationOp.budget.Store(int64(200 * time.Millisecond))
	a.commitHook = func() { time.Sleep(300 * time.Millisecond) }
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":`+strconv.FormatBool(!before.Apps.Weather.Moon)+`}}}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "ran out of time") {
		t.Fatalf("PUT = %d %s, want 503 out of time", resp.StatusCode, b)
	}
	if c := a.composeClockConfig(); c.Apps.Weather.Moon != before.Apps.Weather.Moon {
		t.Fatal("settings committed after the deadline passed")
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockRotationBudgetAnswers503Then502ThenFreesTheLock(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	first := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["date","time"]}`)
	waitEntered(t, stub.orderIn, "first order write")
	a.rotationOp.budget.Store(int64(100 * time.Millisecond))
	epoch := a.devices.epochValue()
	busy := waitCode(t, "waiting order", clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["time","date"]}`))
	if busy.code != http.StatusServiceUnavailable || !strings.Contains(string(busy.body), "still running") {
		t.Fatalf("waiting order = %d %s, want 503", busy.code, busy.body)
	}
	if n := len(stub.puts()); n != 0 || a.devices.epochValue() != epoch {
		t.Fatalf("a 503 wrote %d orders or moved the epoch", n)
	}
	release()
	mustReply(t, "first order", first)
	releaseCut := holdClockOrder(t, stub)
	cut := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["time","date"]}`)
	waitEntered(t, stub.orderIn, "cut order write")
	if r := waitCode(t, "cut order", cut); r.code != http.StatusBadGateway {
		t.Fatalf("cut-short order = %d %s, want 502", r.code, r.body)
	}
	if r := a.clockRotation.Load(); r != nil {
		t.Fatalf("rotation = %+v, want null after a write that may have landed", r)
	}
	assertClockRecordInStep(t, a, srv)
	releaseCut()
	a.rotationOp.budget.Store(int64(clockRotationOpBudget))
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "order after the timeouts", resp, b)
}

func TestClockConfigPutOutOfTimeBeforeCommitChangesNoSettings(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	before, _ := getClockConfig(t, srv, d.ID)
	stub.mu.Lock()
	stub.appsSlow = []time.Duration{0, 5 * time.Second}
	stub.mu.Unlock()
	a.rotationOp.budget.Store(int64(300 * time.Millisecond))
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":`+strconv.FormatBool(!before.Apps.Weather.Moon)+`}},"rotation":{"order":["date","time"]}}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "already written") {
		t.Fatalf("PUT = %d %s, want 503 naming the written order", resp.StatusCode, b)
	}
	if c := a.composeClockConfig(); c.Apps.Weather.Moon != before.Apps.Weather.Moon || c.Rotation != nil {
		t.Fatalf("settings changed or rotation kept after running out of time: %+v", c)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockRotationExpiredBeforeLockIsRejected(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	a.rotationOp.budget.Store(0)
	for i := range 20 {
		resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("try %d: PUT = %d %s, want 503 for an already expired request", i, resp.StatusCode, b)
		}
		resp, b = clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}}}`)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("try %d: facade PUT = %d %s, want 503", i, resp.StatusCode, b)
		}
	}
	if n := len(stub.puts()); n != 0 {
		t.Fatalf("expired requests wrote %d orders", n)
	}
}
