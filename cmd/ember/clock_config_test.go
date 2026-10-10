package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestClockConfigVersionIsHashOfComposedConfig(t *testing.T) {
	a, srv, _ := newClockApp(t)
	d := registeredClock(t, a, srv)
	c, v := getClockConfig(t, srv, d.ID)
	if v != clockConfigHash(c) || v != listDevices(t, srv)[0].ConfigVersion {
		t.Fatalf("header %d, hash %d, record %d", v, clockConfigHash(c), listDevices(t, srv)[0].ConfigVersion)
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

	for _, key := range []string{pomodoroSettingsKey, weatherSettingsKey, meetingsSettingsKey, usageSettingsKey, hiddenAppsKey} {
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
	fc, fv := getClockConfig(t, fsrv, fd.ID)
	lc, lv := getClockConfig(t, lsrv, fd.ID)
	if !reflect.DeepEqual(fc, lc) || fv != lv {
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
	if v, _ := strconv.Atoi(resp.Header.Get(deviceConfigVersion)); v != clockConfigHash(c) {
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
	done := make(chan []byte)
	go func() {
		_, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"calendar":{"tile_lead_minutes":15}},"rotation":{"order":["date","time"]}}`)
		done <- b
	}()
	<-stub.orderIn
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"moon_phase":false}`)
	mustOK(t, "weather put", resp, b)
	resp, b = devReq(t, srv, "PUT", "/v1/apps", testToken, `{"app":"codex","enabled":false}`)
	mustOK(t, "apps put", resp, b)
	release()
	<-done
	c, _ := getClockConfig(t, srv, d.ID)
	if c.Apps.Weather.Moon || c.Apps.Calendar.TileLeadMinutes != 15 || !slices.Equal(c.Apps.Agents.HiddenTools, []string{"codex"}) {
		t.Fatalf("lost update: %+v", c.Apps)
	}
	blob, _, _ := a.store.GetSetting(weatherSettingsKey)
	if !strings.Contains(blob, `"moon_phase":false`) {
		t.Fatalf("stored weather lost moon_phase: %s", blob)
	}
}

func TestClockConfigConcurrentFacadePutsKeepBoth(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	done := make(chan struct{})
	go func() {
		defer close(done)
		clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"calendar":{"tile_lead_minutes":15}},"rotation":{"order":["date","time"]}}`)
	}()
	<-stub.orderIn
	resp, b := clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false},"agents":{"usage_cards":false}}}`)
	mustOK(t, "second put", resp, b)
	release()
	<-done
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
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, op := range []string{"INSERT", "UPDATE"} {
		if _, err := db.Exec(`CREATE TRIGGER fail_weather_` + op + ` BEFORE ` + op + ` ON settings WHEN NEW.key = 'weather_json' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
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

func TestClockRotationStaleReadDoesNotOverwriteNewerWrite(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	in, release := stub.holdApps(t, http.StatusOK)
	done := make(chan struct{})
	go func() {
		defer close(done)
		devReq(t, srv, "GET", "/v1/device/apps", testToken, "")
	}()
	<-in
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	epoch := a.devices.epochValue()
	release()
	<-done
	if r := a.clockRotation.Load(); r == nil || r.Order[0] != "date" || a.devices.epochValue() != epoch {
		t.Fatalf("rotation = %+v epoch %d (want %d): a read sent before the write overwrote it", r, a.devices.epochValue(), epoch)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockRotationFailedOlderReadbackKeepsNewerList(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	in, release := stub.holdApps(t, http.StatusInternalServerError)
	done := make(chan struct{})
	go func() {
		defer close(done)
		devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["time","date"]}`)
	}()
	<-in
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	release()
	<-done
	if r := a.clockRotation.Load(); r == nil || r.Order[0] != "date" {
		t.Fatalf("rotation = %+v: an older failed read-back cleared the newer list", r)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestClockConfigOldRouteDuringFacadePutKeepsRecordInStep(t *testing.T) {
	a, srv, stub := newClockApp(t)
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	done := make(chan struct{})
	go func() {
		defer close(done)
		clockConfigReq(t, srv, "PUT", d.ID, `{"apps":{"weather":{"moon":false}},"rotation":{"order":["date","time"]}}`)
	}()
	<-stub.orderIn
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps", testToken, `{"order":["date","time"]}`)
	mustOK(t, "apps order", resp, b)
	_, v := getClockConfig(t, srv, d.ID)
	if rec := listDevices(t, srv)[0].ConfigVersion; v != rec {
		t.Fatalf("while a facade PUT waits on the clock: header version %d, record %d", v, rec)
	}
	release()
	<-done
	_, v = getClockConfig(t, srv, d.ID)
	if rec := listDevices(t, srv)[0].ConfigVersion; v != rec {
		t.Fatalf("after the facade PUT: header version %d, record %d", v, rec)
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
