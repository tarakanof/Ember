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

// fakeSensorDevice emulates the clock's LittleFS file routes (/dev.json,
// POST /edit) and /api/reboot, recording what the handlers send it.
type fakeSensorDevice struct {
	mu       sync.Mutex
	devJSON  string // current /dev.json body; "" means 404
	uploaded string // last /edit upload body
	uploadAs string // filename of the last uploaded part
	rebooted bool
	failEdit bool
}

func (f *fakeSensorDevice) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/dev.json":
			if f.devJSON == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, f.devJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/edit":
			if f.failEdit {
				http.Error(w, "nope", http.StatusInternalServerError)
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, headers := range r.MultipartForm.File {
				for _, h := range headers {
					// Go's multipart parser strips the directory from the
					// filename; the raw Content-Disposition keeps the full
					// path the device would see.
					f.uploadAs = h.Header.Get("Content-Disposition")
					file, err := h.Open()
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					b, _ := io.ReadAll(file)
					file.Close()
					f.uploaded = string(b)
				}
			}
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/reboot":
			f.rebooted = true
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

func TestDeviceSensorsGetReadsDevJSON(t *testing.T) {
	dev := &fakeSensorDevice{devJSON: `{"temp_offset":-7,"button_callback":"http://e/hooks"}`}
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
	if got.HumOffset != nil {
		t.Fatalf("hum_offset=%v want null", *got.HumOffset)
	}
}

func TestDeviceSensorsGetMissingFile(t *testing.T) {
	dev := &fakeSensorDevice{} // no dev.json on the clock yet
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsGet(w, httptest.NewRequest("GET", "/v1/device/sensors", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"temp_offset":null`) || !strings.Contains(body, `"hum_offset":null`) {
		t.Fatalf("body=%s want both offsets null", body)
	}
}

func TestDeviceSensorsPutMergesUploadsReboots(t *testing.T) {
	dev := &fakeSensorDevice{devJSON: `{"temp_offset":-9,"hum_offset":4,"button_callback":"http://e/hooks"}`}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":-7.5,"hum_offset":null}`))
	a.handleDeviceSensorsPut(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(dev.uploaded), &merged); err != nil {
		t.Fatalf("uploaded body %q: %v", dev.uploaded, err)
	}
	if !strings.Contains(dev.uploadAs, `filename="/dev.json"`) {
		t.Fatalf("uploaded as %q want filename=\"/dev.json\"", dev.uploadAs)
	}
	if got := merged["temp_offset"]; got != -7.5 {
		t.Fatalf("temp_offset=%v want -7.5", got)
	}
	if _, present := merged["hum_offset"]; present {
		t.Fatalf("hum_offset still present after null: %s", dev.uploaded)
	}
	if got := merged["button_callback"]; got != "http://e/hooks" {
		t.Fatalf("button_callback=%v — unrelated dev.json keys must survive", got)
	}
	if !dev.rebooted {
		t.Fatal("device was not rebooted after a successful write")
	}
	if !strings.Contains(w.Body.String(), `"rebooted":true`) {
		t.Fatalf("body=%s want rebooted:true", w.Body.String())
	}
}

func TestDeviceSensorsPutCreatesDevJSONWhenAbsent(t *testing.T) {
	dev := &fakeSensorDevice{} // no dev.json yet
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":-4}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(dev.uploaded), &merged); err != nil {
		t.Fatalf("uploaded body %q: %v", dev.uploaded, err)
	}
	if got := merged["temp_offset"]; got != -4.0 {
		t.Fatalf("temp_offset=%v want -4", got)
	}
}

func TestDeviceSensorsPutValidation(t *testing.T) {
	dev := &fakeSensorDevice{}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	for name, body := range map[string]string{
		"out of range":  `{"temp_offset":120}`,
		"wrong type":    `{"hum_offset":"3"}`,
		"unknown key":   `{"sensor_reading":false}`,
		"empty payload": `{}`,
	} {
		w := httptest.NewRecorder()
		a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d want 400", name, w.Code)
		}
	}
	if dev.uploaded != "" || dev.rebooted {
		t.Fatal("invalid payloads must not touch the device")
	}
}

func TestDeviceSensorsPutNoRebootOnUploadFailure(t *testing.T) {
	dev := &fakeSensorDevice{devJSON: `{}`, failEdit: true}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors",
		strings.NewReader(`{"temp_offset":-4}`)))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502", w.Code)
	}
	if dev.rebooted {
		t.Fatal("must not reboot when the dev.json upload failed")
	}
}
