package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnobWeatherMomentsDefaultOnEveryHalfHour(t *testing.T) {
	d := defaultKnobSettings()
	if d.Bot.WeatherMoments == nil || !*d.Bot.WeatherMoments || d.Bot.WeatherEveryMin == nil || *d.Bot.WeatherEveryMin != 30 {
		t.Fatalf("defaults: %+v", d.Bot)
	}
	var s knobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.fillDefaults()
	if s.Bot.WeatherMoments == nil || !*s.Bot.WeatherMoments || s.Bot.WeatherEveryMin == nil || *s.Bot.WeatherEveryMin != 30 {
		t.Fatalf("a stored record without them must read on, every 30 min: %+v", s.Bot)
	}
}

func TestKnobWeatherMomentsMergeKeepZeroAndNullReadsDefault(t *testing.T) {
	cur := defaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"bot":{"weather_every_min":0}}`))
	if err != nil || got.Bot.WeatherEveryMin == nil || *got.Bot.WeatherEveryMin != 0 || !*got.Bot.WeatherMoments {
		t.Fatalf("merge 0: %v %+v", err, got.Bot)
	}
	if *cur.Bot.WeatherEveryMin != 30 {
		t.Fatal("merging into a clone wrote through to the original")
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"bot":{"weather_moments":false}}`))
	if err != nil || *got.Bot.WeatherMoments || *got.Bot.WeatherEveryMin != 0 {
		t.Fatalf("merge off: %v %+v", err, got.Bot)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"bot":{"weather_moments":null,"weather_every_min":null}}`))
	if err != nil || !*got.Bot.WeatherMoments || *got.Bot.WeatherEveryMin != 30 {
		t.Fatalf("null must read as the defaults: %v %+v", err, got.Bot)
	}
}

func TestKnobWeatherMomentsReachTheKnob(t *testing.T) {
	f := newViewFixture(t)
	_, version, err := f.app.devices.versions(f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"bot":{"weather_moments":false,"weather_every_min":0}}`)
	want := `"working_ring":true,"weather_moments":false,"weather_every_min":0}`
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/self/config", f.m.Token, "")
	if mustOK(t, "self config", resp, b); !strings.Contains(string(b), want) {
		t.Fatalf("self config = %s, want %s", b, want)
	}
	resp, b = devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, capsCheckinBody(t, "0.10.0", version, nil))
	if mustOK(t, "checkin", resp, b); !strings.Contains(string(b), want) {
		t.Fatalf("checkin reply = %s, want config with %s", b, want)
	}
}

func TestKnobWeatherMomentsRejectInvalidValues(t *testing.T) {
	f := newViewFixture(t)
	for _, body := range []string{`{"bot":{"weather_every_min":241}}`, `{"bot":{"weather_every_min":-1}}`,
		`{"bot":{"weather_every_min":"30"}}`, `{"bot":{"weather_every_min":2.5}}`, `{"bot":{"weather_moments":"on"}}`} {
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"bot":{"weather_every_min":240}}`)
}

func knobWeatherMomentsAfterReload(t *testing.T, db, id string) (bool, int, int) {
	t.Helper()
	app, srv := newDevicesApp(t, db)
	defer srv.Close()
	defer func() { _ = app.store.Close() }()
	_, version, err := app.devices.versions(id)
	if err != nil {
		t.Fatal(err)
	}
	resp, b := devReq(t, srv, "GET", "/v1/devices/"+id+"/config", testToken, "")
	mustOK(t, "config get", resp, b)
	var cfg struct {
		Bot struct {
			WeatherMoments  *bool `json:"weather_moments"`
			WeatherEveryMin *int  `json:"weather_every_min"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.Bot.WeatherMoments == nil || cfg.Bot.WeatherEveryMin == nil {
		t.Fatalf("config after reload = %s (%v)", b, err)
	}
	return *cfg.Bot.WeatherMoments, *cfg.Bot.WeatherEveryMin, version
}

func TestKnobWeatherMomentsSurviveARegistryReload(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app, srv := newDevicesApp(t, db)
	knob := mintKnob(t, srv, http.StatusCreated)
	putKnobConfig(t, srv, knob.ID, `{"bot":{"weather_moments":false,"weather_every_min":0}}`)
	_, version, err := app.devices.versions(knob.ID)
	if err != nil {
		t.Fatal(err)
	}
	blob, _, err := app.store.GetSetting(devicesKey)
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	_ = app.store.Close()

	if on, every, v := knobWeatherMomentsAfterReload(t, db, knob.ID); on || every != 0 || v != version {
		t.Fatalf("explicit off/0 after reload = %v/%d version %d, want false/0 version %d", on, every, v, version)
	}

	const stored = `,"weather_moments":false,"weather_every_min":0`
	if !strings.Contains(blob, stored) {
		t.Fatalf("stored record without %s: %s", stored, blob)
	}
	app, srv = newDevicesApp(t, db)
	if err := app.store.PutSetting(devicesKey, strings.Replace(blob, stored, "", 1)); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	_ = app.store.Close()

	if on, every, v := knobWeatherMomentsAfterReload(t, db, knob.ID); !on || every != 30 || v != version {
		t.Fatalf("a record stored without the keys reloads as %v/%d version %d, want true/30 version %d (no push)", on, every, v, version)
	}
}
