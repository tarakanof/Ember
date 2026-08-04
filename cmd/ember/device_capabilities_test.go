package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCapabilitiesJSON = `{"effects":["Fade","Matrix","Snake"],"paletteEffects":["Fade"],
	"transitions":["Slide","Dim"],"overlays":["rain"],"palettes":["Ocean"],
	"radio":false,"gpio":{"soc":"esp32","label":"ESP32","max":39}}`

// fakeClockDevice serves the two endpoints refreshCapabilities reads, counting hits.
func fakeClockDevice(t *testing.T, capsStatus int) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/capabilities":
			hits++
			w.WriteHeader(capsStatus)
			if capsStatus == http.StatusOK {
				_, _ = w.Write([]byte(testCapabilitiesJSON))
			}
		case "/api/v1/device":
			_, _ = w.Write([]byte(`{"version":"1.0.13","uid":"abc","boardType":"awtrixng"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// capsApp points an app at a fake clock.
func capsApp(t *testing.T, baseURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = baseURL
	cfg.applyDefaults()
	return NewApp(cfg, &recordingPublisher{}, testLogger())
}

func TestRefreshCapabilitiesCachesFirmwareAndLists(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())

	caps, ok := app.capabilities()
	if !ok {
		t.Fatal("capabilities not cached")
	}
	if len(caps.Effects) != 3 || len(caps.Transitions) != 2 {
		t.Errorf("caps = %+v", caps)
	}
	if got := app.deviceFirmware(); got != "1.0.13" {
		t.Errorf("firmware = %q, want 1.0.13", got)
	}
}

func TestRefreshCapabilitiesLeavesCacheEmptyOnFailure(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusInternalServerError)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())
	if _, ok := app.capabilities(); ok {
		t.Fatal("a failed fetch must not populate the cache")
	}
}

func TestDeviceCapabilitiesEndpointServesCacheWithoutTouchingDevice(t *testing.T) {
	srv, hits := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())
	before := *hits

	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if *hits != before {
		t.Errorf("a cached read must not hit the clock (%d → %d)", before, *hits)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, w.Body.String())
	}
	for _, k := range []string{"effects", "paletteEffects", "transitions", "overlays", "palettes", "radio", "gpio"} {
		if _, ok := got[k]; !ok {
			t.Errorf("response missing %q: %s", k, w.Body.String())
		}
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestDeviceCapabilitiesEndpointFallsBackToLiveFetch(t *testing.T) {
	srv, hits := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)

	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if *hits != 1 {
		t.Errorf("empty cache should proxy once, got %d hits", *hits)
	}
	if !strings.Contains(w.Body.String(), `"paletteEffects"`) {
		t.Errorf("body = %s", w.Body.String())
	}
	// The live fetch also warms the cache, so the next read is free.
	w2 := httptest.NewRecorder()
	app.handleDeviceCapabilities(w2, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if *hits != 1 {
		t.Errorf("second read should be served from cache, got %d hits", *hits)
	}
}

func TestDeviceCapabilitiesEndpointBadGatewayWhenClockUnreachable(t *testing.T) {
	app := capsApp(t, "http://127.0.0.1:1")
	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestDoctorReportsCapabilityCounts(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	c, ok := res.Checks["capabilities"]
	if !ok {
		t.Fatal("doctor has no capabilities check")
	}
	if c.Status != StatusOK {
		t.Fatalf("status = %q detail = %q", c.Status, c.Detail)
	}
	for _, want := range []string{"effects=3", "transitions=2", "radio=false", "firmware=1.0.13"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q missing %q", c.Detail, want)
		}
	}
}

func TestDoctorWarnsWhenCapabilitiesNotFetched(t *testing.T) {
	app := capsApp(t, "http://127.0.0.1:1")
	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	if got := res.Checks["capabilities"].Status; got != StatusWarn {
		t.Fatalf("status = %q, want warn", got)
	}
}
