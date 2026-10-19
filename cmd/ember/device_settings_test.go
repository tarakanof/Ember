package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tarakanof/ember/internal/awtrix"
)

func TestValidateDeviceSettings(t *testing.T) {
	ok := map[string]any{
		"brightness": float64(128), "buzzerVolume": float64(100), "soundEnabled": false, "autoBrightness": true,
		"transitionEffect": "Rain", "textColor": "#FF8800", "uppercase": true,
		"timeMode": float64(2), "calendarHeaderColor": []any{float64(255), float64(0), float64(0)},
		"appDurationMs": float64(7000), "transitionDurationMs": float64(1000),
		"time24h": true, "timeLeadingZero": true, "timeShowSeconds": false, "timeShowAmPm": false,
		"timeSeparatorMode": "pulse",
		"dateOrder":         "dayMonthYear", "dateSeparator": "dot", "dateYearMode": "twoDigit",
		"dateShowWeekday": false, "dateMonthNames": false,
		"autoTransition": true, "blockNavigation": false,
		"timeColor": "#FFFFFF", "dateColor": "#FFFFFF", "temperatureColor": "#FFFFFF",
		"humidityColor": "#FFFFFF", "batteryColor": "#FFFFFF",
		"useCelsius": true,
		"scroll": map[string]any{
			"mode": "wrap", "direction": "left", "entry": "inline", "whenFits": "static",
			"speed": float64(100), "gap": float64(8), "holdMs": float64(1000),
		},
		"weekdayBar": map[string]any{
			"show": true, "startOnMonday": true,
			"activeColor": "#FFFFFF", "inactiveColor": "#666666",
		},
	}
	if err := validateDeviceSettings(ok, soundAPILegacy); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	inherit := map[string]any{
		"timeColor": nil, "dateColor": nil, "temperatureColor": nil,
		"humidityColor": nil, "batteryColor": nil,
	}
	if err := validateDeviceSettings(inherit, soundAPILegacy); err != nil {
		t.Fatalf("null per-app colours rejected: %v", err)
	}
	bad := []map[string]any{
		{"brightness": float64(999)},
		{"buzzerVolume": float64(101)},
		{"NOPE": true},
		{"autoBrightness": "yes"},
		{"textColor": "purple"},
		{"timeMode": float64(9)},
		{"brightness": float64(12.5)},
		{"timeSeparatorMode": "rainbow"},
		{"dateOrder": "nope"},
		{"transitionEffect": strings.Repeat("x", 33)},
		{"calendarHeaderColor": []any{float64(255), float64(0)}},
		{"calendarHeaderColor": []any{float64(300), float64(0), float64(0)}},
		{"appDurationMs": float64(500)},
		{"scroll": map[string]any{"speed": "fast"}},
		{"scroll": map[string]any{"nope": true}},
		{"scroll": "not-an-object"},
		{"weekdayBar": map[string]any{"activeColor": "purple"}},
		{"weekdayBar": map[string]any{"weekendDays": []any{"sunday"}}},
		{"timeColor": "purple"},
		{"useCelsius": "yes"},
		{"soundEnabled": "yes"},
		{"buzzerVolume": float64(-1)},
		{"volume": float64(10)},
		{"smoothScroll": true},
		{"textColor": nil},
		{"calendarBodyColor": nil},
		{"weekdayBar": map[string]any{"activeColor": nil}},
	}
	for i, b := range bad {
		if err := validateDeviceSettings(b, soundAPILegacy); err == nil {
			t.Fatalf("case %d: expected rejection for %v", i, b)
		}
	}
}

func TestDeviceSettingsProxyForwardsAndFilters(t *testing.T) {
	var gotBody string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/settings":
			if r.Method == http.MethodPatch {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.WriteHeader(200)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"brightness":120,"volume":8,"soundEnabled":true,"buzzerVolume":80,"dfplayerVolume":80,"NOPE":"x","MATP":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer dev.Close()

	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}

	gw := httptest.NewRecorder()
	a.handleDeviceSettingsGet(gw, httptest.NewRequest("GET", "/v1/device/settings", nil))
	if gw.Code != 200 {
		t.Fatalf("get code=%d body=%s", gw.Code, gw.Body.String())
	}
	if strings.Contains(gw.Body.String(), "NOPE") || strings.Contains(gw.Body.String(), "MATP") || strings.Contains(gw.Body.String(), "dfplayerVolume") {
		t.Fatalf("get leaked non-whitelisted keys: %s", gw.Body.String())
	}
	if !strings.Contains(gw.Body.String(), "brightness") || !strings.Contains(gw.Body.String(), "soundEnabled") || !strings.Contains(gw.Body.String(), "buzzerVolume") {
		t.Fatalf("get dropped whitelisted key: %s", gw.Body.String())
	}

	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"brightness":128}`)))
	if pw.Code != 200 || !strings.Contains(gotBody, "128") {
		t.Fatalf("put code=%d forwarded=%q", pw.Code, gotBody)
	}

	bw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(bw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"brightness":999}`)))
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("bad put code=%d want 400", bw.Code)
	}
}

func TestDeviceActionsProxy(t *testing.T) {
	var hits []string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.WriteHeader(200)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceReboot(rw, httptest.NewRequest("POST", "/v1/device/reboot", nil))
	dw := httptest.NewRecorder()
	a.handleDeviceDismiss(dw, httptest.NewRequest("POST", "/v1/device/notify/dismiss", nil))
	nw := httptest.NewRecorder()
	a.handleDeviceNextApp(nw, httptest.NewRequest("POST", "/v1/device/app/next", nil))
	pw := httptest.NewRecorder()
	a.handleDevicePrevApp(pw, httptest.NewRequest("POST", "/v1/device/app/previous", nil))
	if rw.Code != 200 || dw.Code != 200 || nw.Code != 200 || pw.Code != 200 {
		t.Fatalf("reboot=%d dismiss=%d next=%d prev=%d", rw.Code, dw.Code, nw.Code, pw.Code)
	}
	joined := strings.Join(hits, ",")
	if !strings.Contains(joined, "/api/v1/device/reboot") || !strings.Contains(joined, "/api/v1/notifications/active") ||
		!strings.Contains(joined, "/api/v1/apps/next") || !strings.Contains(joined, "/api/v1/apps/previous") {
		t.Fatalf("device endpoints hit: %v", hits)
	}
}

func TestDeviceProxyMapsDeviceErrorTo502(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewRecorder()
	a.handleDeviceSettingsGet(gw, httptest.NewRequest("GET", "/v1/device/settings", nil))
	if gw.Code != http.StatusBadGateway {
		t.Fatalf("get code=%d want 502", gw.Code)
	}
	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"brightness":120}`)))
	if pw.Code != http.StatusBadGateway {
		t.Fatalf("put code=%d want 502", pw.Code)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceReboot(rw, httptest.NewRequest("POST", "/v1/device/reboot", nil))
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("reboot code=%d want 502", rw.Code)
	}
}

func TestDeviceSettingsNoClockConfigured(t *testing.T) {
	a := newTestAppWithStore(t)
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = ""
	a.cfg.Store(&cur)
	w := httptest.NewRecorder()
	a.handleDeviceSettingsGet(w, httptest.NewRequest("GET", "/v1/device/settings", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code=%d want 502", w.Code)
	}
}

func TestDeviceScreenProxy(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/display/screen" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[16711680,0,255]`))
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceScreen(rw, httptest.NewRequest("GET", "/v1/device/screen", nil))
	if rw.Code != 200 {
		t.Fatalf("code=%d want 200", rw.Code)
	}
	if got := strings.TrimSpace(rw.Body.String()); got != `[16711680,0,255]` {
		t.Fatalf("body=%q", got)
	}
	if ct := rw.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type=%q", ct)
	}
}

func TestDeviceScreenProxyMapsErrorTo502(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceScreen(rw, httptest.NewRequest("GET", "/v1/device/screen", nil))
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("code=%d want 502", rw.Code)
	}
}

func cutBodyClock(t *testing.T) string {
	t.Helper()
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cutReply(w, http.StatusOK, "[16711680,")
	}))
	t.Cleanup(dev.Close)
	return dev.URL
}

func TestDeviceScreenProxyMapsCutBodyTo502(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, cutBodyClock(t)); err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceScreen(rw, httptest.NewRequest("GET", "/v1/device/screen", nil))
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("code=%d body=%q, want 502", rw.Code, rw.Body.String())
	}
}

func TestDeviceSettingsDuringTakeoverUsePrior(t *testing.T) {
	var patches []string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/settings" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			patches = append(patches, string(b))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"brightness":120,"autoTransition":false,"blockNavigation":true}`))
	}))
	defer dev.Close()

	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	startTestTakeover(a, takeoverPrior{AutoTransition: true, BlockNavigation: false})

	gw := httptest.NewRecorder()
	a.handleDeviceSettingsGet(gw, httptest.NewRequest("GET", "/v1/device/settings", nil))
	var got map[string]any
	if err := json.Unmarshal(gw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["autoTransition"] != true || got["blockNavigation"] != false || got["brightness"] != 120.0 {
		t.Fatalf("get during takeover = %v, want the prior's values", got)
	}
	if h := gw.Header().Get(deferredKeysHeader); h != "autoTransition,blockNavigation" {
		t.Fatalf("get %s = %q", deferredKeysHeader, h)
	}

	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings",
		strings.NewReader(`{"autoTransition":false,"brightness":64}`)))
	if pw.Code != http.StatusOK {
		t.Fatalf("put code=%d body=%s", pw.Code, pw.Body.String())
	}
	if len(patches) != 1 || patches[0] != `{"brightness":64}` {
		t.Fatalf("device patches = %q, want only brightness", patches)
	}
	if h := pw.Header().Get(deferredKeysHeader); h != "autoTransition" {
		t.Fatalf("put %s = %q", deferredKeysHeader, h)
	}
	if p, _ := priorView(a.coord); p.AutoTransition || p.BlockNavigation {
		t.Fatalf("prior = %+v, want autoTransition:false blockNavigation:false", p)
	}
	if v, _, _ := a.store.GetSetting(takeoverPriorKey); !strings.Contains(v, `"autoTransition":false`) {
		t.Fatalf("persisted prior = %q, want the edit", v)
	}
}

func startTestTakeover(a *App, p takeoverPrior) {
	a.coord.priorMu.Lock()
	defer a.coord.priorMu.Unlock()
	a.coord.setPrior(&p)
}

func TestDeviceSettingsPutDuringTakeoverFailureKeepsPrior(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	startTestTakeover(a, takeoverPrior{AutoTransition: true})
	stored, _, _ := a.store.GetSetting(takeoverPriorKey)

	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings",
		strings.NewReader(`{"autoTransition":false,"brightness":64}`)))
	if pw.Code != http.StatusBadGateway {
		t.Fatalf("put code=%d want 502", pw.Code)
	}
	if h := pw.Header().Get(deferredKeysHeader); h != "" {
		t.Fatalf("%s = %q on a failed save", deferredKeysHeader, h)
	}
	if p, _ := priorView(a.coord); !p.AutoTransition {
		t.Fatalf("prior = %+v, want unchanged", p)
	}
	if v, _, _ := a.store.GetSetting(takeoverPriorKey); v != stored {
		t.Fatalf("stored prior = %q, want unchanged %q", v, stored)
	}
}

func TestValidateDeviceSettingsSoundKeysFollowAudioAPI(t *testing.T) {
	mixer := map[string]any{"volume": float64(60), "appVolume": float64(100), "alertVolume": float64(0)}
	legacy := map[string]any{"soundEnabled": false, "buzzerVolume": float64(80)}
	if err := validateDeviceSettings(mixer, soundAPIMixer); err != nil {
		t.Fatalf("ng12 rejected mixer keys: %v", err)
	}
	if err := validateDeviceSettings(legacy, soundAPILegacy); err != nil {
		t.Fatalf("ng11 rejected legacy keys: %v", err)
	}
	for _, m := range []map[string]any{mixer, legacy} {
		if err := validateDeviceSettings(m, soundAPIUnknown); err != nil {
			t.Fatalf("unknown API rejected %v: %v", m, err)
		}
	}
	for k := range legacy {
		err := validateDeviceSettings(map[string]any{k: legacy[k]}, soundAPIMixer)
		if err == nil || !strings.Contains(err.Error(), "volume, appVolume or alertVolume") {
			t.Errorf("ng12 %s: err = %v, want it to name the replacement keys", k, err)
		}
	}
	for k := range mixer {
		err := validateDeviceSettings(map[string]any{k: mixer[k]}, soundAPILegacy)
		if err == nil || !strings.Contains(err.Error(), "soundEnabled and buzzerVolume") {
			t.Errorf("ng11 %s: err = %v", k, err)
		}
	}
	for _, b := range []map[string]any{{"volume": float64(101)}, {"appVolume": float64(-1)}, {"alertVolume": float64(1.5)}, {"volume": true}} {
		if err := validateDeviceSettings(b, soundAPIUnknown); err == nil {
			t.Errorf("accepted %v", b)
		}
	}
}

func TestSoundAPIOfIgnoresAStrayVolume(t *testing.T) {
	cases := []struct {
		settings map[string]any
		want     soundAPI
	}{
		{map[string]any{"volume": 60.0, "appVolume": 100.0, "alertVolume": 40.0}, soundAPIMixer},
		{map[string]any{"alertVolume": 40.0}, soundAPIMixer},
		{map[string]any{"volume": 8.0, "soundEnabled": true, "buzzerVolume": 80.0}, soundAPILegacy},
		{map[string]any{"volume": 8.0}, soundAPIUnknown},
		{map[string]any{"brightness": 10.0}, soundAPIUnknown},
	}
	for _, c := range cases {
		if got := soundAPIOf(c.settings); got != c.want {
			t.Errorf("soundAPIOf(%v) = %v, want %v", c.settings, got, c.want)
		}
	}
}

func settingsClock(t *testing.T, reply string) (*App, *string) {
	t.Helper()
	var mu sync.Mutex
	var gotBody string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/settings" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			gotBody = string(b)
			mu.Unlock()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(dev.Close)
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	return a, &gotBody
}

const ng12SettingsReply = `{"brightness":120,"volume":60,"appVolume":100,"alertVolume":40,"radioVolume":80}`
const ng11SettingsReply = `{"brightness":120,"soundEnabled":true,"buzzerVolume":80}`

func getDeviceSettings(t *testing.T, a *App) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	a.handleDeviceSettingsGet(w, httptest.NewRequest("GET", "/v1/device/settings", nil))
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	return got
}

func putDeviceSettings(a *App, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.handleDeviceSettingsPut(w, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(body)))
	return w
}

func TestDeviceSettingsGetPassesTheClocksSoundKeysWithoutCaps(t *testing.T) {
	for _, stale := range []bool{false, true} {
		a, _ := settingsClock(t, ng12SettingsReply)
		if stale {
			withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
		}
		got := getDeviceSettings(t, a)
		if got["volume"] != float64(60) || got["appVolume"] != float64(100) || got["alertVolume"] != float64(40) {
			t.Errorf("stale=%v: mixer keys missing: %v", stale, got)
		}
		if _, has := got["radioVolume"]; has {
			t.Errorf("radioVolume passed: %v", got)
		}
	}
}

func TestDeviceSettingsPutFollowsTheKeysTheClockReported(t *testing.T) {
	a, sent := settingsClock(t, ng12SettingsReply)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	getDeviceSettings(t, a)
	if w := putDeviceSettings(a, `{"alertVolume":0}`); w.Code != 200 || *sent != `{"alertVolume":0}` {
		t.Fatalf("code=%d body=%s forwarded=%q", w.Code, w.Body.String(), *sent)
	}
	*sent = ""
	for _, body := range []string{`{"soundEnabled":false}`, `{"buzzerVolume":50}`} {
		w := putDeviceSettings(a, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "volume, appVolume or alertVolume") || *sent != "" {
			t.Fatalf("%s: code=%d body=%s forwarded=%q", body, w.Code, w.Body.String(), *sent)
		}
	}
}

func TestDeviceSettingsPutFallsBackToCaps(t *testing.T) {
	a, sent := settingsClock(t, ng11SettingsReply)
	withAudioCaps(a, ng12Audio(true))
	if w := putDeviceSettings(a, `{"soundEnabled":false}`); w.Code != http.StatusBadRequest || *sent != "" {
		t.Fatalf("ng12 caps: code=%d forwarded=%q", w.Code, *sent)
	}
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	if w := putDeviceSettings(a, `{"volume":10}`); w.Code != http.StatusBadRequest || *sent != "" {
		t.Fatalf("ng11 caps: code=%d forwarded=%q", w.Code, *sent)
	}
}

func TestDeviceSettingsPutWithNothingKnownLetsTheClockDecide(t *testing.T) {
	for _, body := range []string{`{"volume":10}`, `{"soundEnabled":false}`} {
		a, sent := settingsClock(t, ng12SettingsReply)
		if w := putDeviceSettings(a, body); w.Code != 200 || *sent != body {
			t.Fatalf("%s: code=%d forwarded=%q", body, w.Code, *sent)
		}
	}
}
