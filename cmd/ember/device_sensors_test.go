package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeSystemDevice emulates the clock's /api/v1/system endpoint, recording
// what the handlers send it. system always reflects the full config, the way
// the real device does — there is no "absent key" state.
type fakeSystemDevice struct {
	mu       sync.Mutex
	system   map[string]any
	lastPut  string // last full PUT /api/v1/system body
	putCount int
	failPut  bool
}

func (f *fakeSystemDevice) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/api/v1/system" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			b, _ := json.Marshal(f.system)
			w.Write(b)
		case http.MethodPut:
			if f.failPut {
				http.Error(w, "nope", http.StatusInternalServerError)
				return
			}
			b, _ := io.ReadAll(r.Body)
			f.lastPut = string(b)
			f.putCount++
			var m map[string]any
			if err := json.Unmarshal(b, &m); err == nil {
				f.system = m
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func sensorTestApp(t *testing.T, deviceURL string) *App {
	t.Helper()
	a := newTestAppWithStore(t)
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = deviceURL
	a.cfg.Store(&cur)
	return a
}

func TestDeviceSensorsGetReadsSystemAPI(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{
		"tempOffset": -7.0, "humOffset": 3.0, "buttonCallback": "http://e/hooks", "wifiSsid": "x",
	}}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsGet(w, httptest.NewRequest("GET", "/v1/device/sensors", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		TempOffset *float64 `json:"temp_offset"`
		HumOffset  *float64 `json:"hum_offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TempOffset == nil || *got.TempOffset != -7 {
		t.Fatalf("temp_offset=%v want -7", got.TempOffset)
	}
	if got.HumOffset == nil || *got.HumOffset != 3 {
		t.Fatalf("hum_offset=%v want 3", got.HumOffset)
	}
}

func TestDeviceSensorsPutMergesFullSystemObjectNoReboot(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{
		"tempOffset": -9.0, "humOffset": 0.0, "buttonCallback": "http://e/hooks", "wifiSsid": "Akitaka",
	}}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":-7.5,"hum_offset":2.5}`))
	a.handleDeviceSensorsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(dev.lastPut), &merged); err != nil {
		t.Fatalf("PUT body %q: %v", dev.lastPut, err)
	}
	if got := merged["tempOffset"]; got != -7.5 {
		t.Fatalf("tempOffset=%v want -7.5", got)
	}
	if got := merged["humOffset"]; got != 2.5 {
		t.Fatalf("humOffset=%v want 2.5", got)
	}
	// Unrelated system keys (notably Wi-Fi credentials) must survive the
	// read-merge-PUT untouched — this is the whole point of not blind-PUTting
	// a partial object.
	if got := merged["buttonCallback"]; got != "http://e/hooks" {
		t.Fatalf("buttonCallback=%v — unrelated system keys must survive", got)
	}
	if got := merged["wifiSsid"]; got != "Akitaka" {
		t.Fatalf("wifiSsid=%v — unrelated system keys must survive", got)
	}
	if dev.putCount != 1 {
		t.Fatalf("putCount=%d want exactly one PUT", dev.putCount)
	}
	// NG applies system changes live; no reboot call was made.
	if strings.Contains(w.Body.String(), "rebooted") {
		t.Fatalf("body=%s must not claim a reboot happened", w.Body.String())
	}
}

func TestDeviceSensorsPutExplicitNullResetsToFirmwareDefault(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{
		"tempOffset": -3.0, "humOffset": 8.0, "buttonCallback": "",
	}}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":null,"hum_offset":null}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(dev.lastPut), &merged); err != nil {
		t.Fatal(err)
	}
	if got := merged["tempOffset"]; got != defaultTempOffset {
		t.Fatalf("tempOffset=%v want firmware default %v", got, defaultTempOffset)
	}
	if got := merged["humOffset"]; got != defaultHumOffset {
		t.Fatalf("humOffset=%v want firmware default %v", got, defaultHumOffset)
	}
}

func TestDeviceSensorsPutValidation(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{"tempOffset": -9.0, "humOffset": 0.0}}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	for name, body := range map[string]string{
		"temp out of range": `{"temp_offset":25}`,
		"hum out of range":  `{"hum_offset":60}`,
		"wrong type":        `{"hum_offset":"3"}`,
		"unknown key":       `{"sensor_reading":false}`,
		"empty payload":     `{}`,
	} {
		w := httptest.NewRecorder()
		a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d want 400", name, w.Code)
		}
	}
	if dev.putCount != 0 {
		t.Fatal("invalid payloads must not touch the device")
	}
}

func TestDeviceSensorsPutFailedPutReturns502AndDoesNotClaimSuccess(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{"tempOffset": -9.0}, failPut: true}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":-4}`)))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502", w.Code)
	}
}
