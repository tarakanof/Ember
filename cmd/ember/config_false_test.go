package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests guard the "config booleans must be able to express false" fix:
// explicit false / 0 in config.json (and in PUT bodies and store blobs) must
// stick, absent fields must resolve to the friendly defaults, and marshalled
// config (GET responses) must keep emitting concrete booleans/ints — never
// nulls — so the menu app's Swift models keep decoding unchanged.

// mustJSONHave asserts each want substring appears in the marshalled JSON.
func mustJSONHave(t *testing.T, blob []byte, wants ...string) {
	t.Helper()
	s := string(blob)
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("marshalled config missing %q in %s", w, s)
		}
	}
	if strings.Contains(s, "null") {
		t.Errorf("marshalled config contains null: %s", s)
	}
}

// TestWeatherFileFalseSticks: a config.json that explicitly disables the
// opt-out toggles (and sets popup_interval_minutes to the documented 0=off)
// must keep them off after applyDefaults — the file-load path.
func TestWeatherFileFalseSticks(t *testing.T) {
	src := `{
		"enabled": true, "rotate_in_apps": false, "forecast_tile": false,
		"popup_on_change": false, "severe_alert": false, "sun_popups": false,
		"moon_phase": false, "air_tile": false, "popup_interval_minutes": 0
	}`
	var c WeatherConfig
	if err := json.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	c.applyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob,
		`"rotate_in_apps":false`, `"forecast_tile":false`, `"popup_on_change":false`,
		`"severe_alert":false`, `"sun_popups":false`, `"moon_phase":false`,
		`"air_tile":false`, `"popup_interval_minutes":0`)
}

// TestWeatherFileAbsentDefaults: fields absent from config.json still resolve
// to the friendly defaults, and the marshalled shape stays concrete (no nulls).
func TestWeatherFileAbsentDefaults(t *testing.T) {
	var c WeatherConfig
	if err := json.Unmarshal([]byte(`{}`), &c); err != nil {
		t.Fatal(err)
	}
	c.applyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob,
		`"rotate_in_apps":true`, `"forecast_tile":true`, `"popup_on_change":true`,
		`"severe_alert":true`, `"sun_popups":true`, `"moon_phase":true`,
		`"air_tile":true`, `"popup_interval_minutes":120`)
}

// TestMeetingsFileFalseSticks: enabled=false, chime=false and the documented
// popup_lead_minutes=0 (popup off) must survive the file-load defaults.
func TestMeetingsFileFalseSticks(t *testing.T) {
	src := `{"enabled": false, "chime": false, "popup_lead_minutes": 0}`
	var c MeetingsConfig
	if err := json.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	c.applyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob, `"enabled":false`, `"chime":false`, `"popup_lead_minutes":0`)
}

// TestMeetingsFileAbsentDefaults: an empty meetings config resolves to the
// defaults (enabled, chime, popup lead 2) with a concrete JSON shape.
func TestMeetingsFileAbsentDefaults(t *testing.T) {
	var c MeetingsConfig
	if err := json.Unmarshal([]byte(`{}`), &c); err != nil {
		t.Fatal(err)
	}
	c.applyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob, `"enabled":true`, `"chime":true`, `"popup_lead_minutes":2`)
}

// TestOldWeatherBlobFillsDefaults: a store blob written by an older build that
// predates newer fields (air_tile, moon_phase, ...) must decode to the same
// effective values as a fresh default config — absent means "the default",
// never "silently off" — while the fields it does carry (including explicit
// false) are honoured verbatim. The GET response must stay null-free.
func TestOldWeatherBlobFillsDefaults(t *testing.T) {
	a := newTestAppWithStore(t)
	oldBlob := `{"enabled":true,"provider":"open-meteo","latitude":52,"longitude":4,` +
		`"units":"metric","refresh_minutes":10,"rotate_in_apps":false,` +
		`"popup_interval_minutes":0,"popup_duration_seconds":30}`
	if err := a.store.PutSetting(weatherSettingsKey, oldBlob); err != nil {
		t.Fatal(err)
	}
	a.loadPersistedWeatherSettings()

	w := httptest.NewRecorder()
	a.handleWeatherConfigGet(w, httptest.NewRequest("GET", "/v1/weather/config", nil))
	if w.Code != 200 {
		t.Fatalf("GET code=%d body=%s", w.Code, w.Body)
	}
	mustJSONHave(t, w.Body.Bytes(),
		// absent in the old blob → today's defaults
		`"forecast_tile":true`, `"popup_on_change":true`, `"severe_alert":true`,
		`"sun_popups":true`, `"moon_phase":true`, `"air_tile":true`,
		// present in the old blob → verbatim, including explicit false/0
		`"rotate_in_apps":false`, `"popup_interval_minutes":0`)
}

// TestOldMeetingsBlobFillsDefaults: same for meetings_json — a blob missing
// chime gets the default (true); its explicit enabled=false sticks.
func TestOldMeetingsBlobFillsDefaults(t *testing.T) {
	a := newTestAppWithStore(t)
	oldBlob := `{"enabled":false,"tile_lead_minutes":60,"popup_lead_minutes":5}`
	if err := a.store.PutSetting(meetingsSettingsKey, oldBlob); err != nil {
		t.Fatal(err)
	}
	a.loadPersistedMeetingsSettings()

	w := httptest.NewRecorder()
	a.handleMeetingsConfigGet(w, httptest.NewRequest("GET", "/v1/meetings/config", nil))
	if w.Code != 200 {
		t.Fatalf("GET code=%d body=%s", w.Code, w.Body)
	}
	mustJSONHave(t, w.Body.Bytes(),
		`"chime":true`, `"enabled":false`, `"popup_lead_minutes":5`)
}

// TestWeatherPutExplicitFalseSticks: the menu app PUTs a full config object
// with explicit booleans — every false must stick and the echoed body (same
// shape as GET) must stay concrete.
func TestWeatherPutExplicitFalseSticks(t *testing.T) {
	a := newTestAppWithStore(t)
	body := `{"enabled":true,"provider":"open-meteo","latitude":52,"longitude":4,` +
		`"location_name":"AMS","units":"metric","refresh_minutes":10,` +
		`"rotate_in_apps":false,"forecast_tile":false,"forecast_hours":24,` +
		`"sun_popups":false,"moon_phase":false,"popup_interval_minutes":0,` +
		`"popup_duration_seconds":30,"popup_on_change":false,"severe_alert":false,` +
		`"severe_sound":"","use_native_icons":false,"tile_native_icons":false,` +
		`"air_tile":false,"air_popup_threshold":0}`
	pw := httptest.NewRecorder()
	a.handleWeatherConfigPut(pw, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(body)))
	if pw.Code != 200 {
		t.Fatalf("PUT code=%d body=%s", pw.Code, pw.Body)
	}
	gw := httptest.NewRecorder()
	a.handleWeatherConfigGet(gw, httptest.NewRequest("GET", "/v1/weather/config", nil))
	mustJSONHave(t, gw.Body.Bytes(),
		`"rotate_in_apps":false`, `"forecast_tile":false`, `"popup_on_change":false`,
		`"severe_alert":false`, `"sun_popups":false`, `"moon_phase":false`,
		`"air_tile":false`, `"popup_interval_minutes":0`)
}

// TestMeetingsPutExplicitFalseSticks: same for the meetings PUT.
func TestMeetingsPutExplicitFalseSticks(t *testing.T) {
	a := newTestAppWithStore(t)
	body := `{"enabled":false,"tile_lead_minutes":60,"popup_lead_minutes":0,"chime":false}`
	pw := httptest.NewRecorder()
	a.handleMeetingsConfigPut(pw, httptest.NewRequest("PUT", "/v1/meetings/config", strings.NewReader(body)))
	if pw.Code != 200 {
		t.Fatalf("PUT code=%d body=%s", pw.Code, pw.Body)
	}
	gw := httptest.NewRecorder()
	a.handleMeetingsConfigGet(gw, httptest.NewRequest("GET", "/v1/meetings/config", nil))
	mustJSONHave(t, gw.Body.Bytes(),
		`"enabled":false`, `"chime":false`, `"popup_lead_minutes":0`)
}
