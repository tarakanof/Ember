package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateDeviceSettings(t *testing.T) {
	ok := map[string]any{
		"BRI": float64(128), "VOL": float64(10), "ABRI": true, "TEFF": float64(3),
		"TCOL": "#FF8800", "TIM": true, "TMODE": float64(2), "CHCOL": []any{float64(255), float64(0), float64(0)},
		"TFORMAT": "%H %M", "OVERLAY": "clear",
	}
	if err := validateDeviceSettings(ok); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	bad := []map[string]any{
		{"BRI": float64(999)},                                  // out of range
		{"VOL": float64(-1)},                                   // out of range
		{"NOPE": true},                                         // unknown key
		{"ABRI": "yes"},                                        // wrong type
		{"TCOL": "purple"},                                     // bad color
		{"TMODE": float64(9)},                                  // out of range
		{"BRI": float64(12.5)},                                 // non-integer
		{"OVERLAY": "rainbow"},                                 // not in enum
		{"TFORMAT": strings.Repeat("x", 32)},                   // too long
		{"CHCOL": []any{float64(255), float64(0)}},             // 2-element array
		{"CHCOL": []any{float64(300), float64(0), float64(0)}}, // component out of range
		{"CHCOL": []any{float64(1), float64(2), float64(3), float64(4)}}, // 4-element array
		{"CHCOL": []any{float64(1.5), float64(0), float64(0)}},           // non-integer component
	}
	for i, b := range bad {
		if err := validateDeviceSettings(b); err == nil {
			t.Fatalf("case %d: expected rejection for %v", i, b)
		}
	}
}

func TestDeviceSettingsProxyForwardsAndFilters(t *testing.T) {
	var gotBody string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/settings":
			if r.Method == http.MethodPost {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.WriteHeader(200)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"BRI":120,"VOL":8,"TIM":true,"NOPE":"x","MATP":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer dev.Close()

	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}

	// GET filters to whitelisted keys only.
	gw := httptest.NewRecorder()
	a.handleDeviceSettingsGet(gw, httptest.NewRequest("GET", "/v1/device/settings", nil))
	if gw.Code != 200 {
		t.Fatalf("get code=%d body=%s", gw.Code, gw.Body.String())
	}
	if strings.Contains(gw.Body.String(), "NOPE") || strings.Contains(gw.Body.String(), "MATP") {
		t.Fatalf("get leaked non-whitelisted keys: %s", gw.Body.String())
	}
	if !strings.Contains(gw.Body.String(), "BRI") {
		t.Fatalf("get dropped whitelisted key: %s", gw.Body.String())
	}

	// PUT validates then forwards.
	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"BRI":128}`)))
	if pw.Code != 200 || !strings.Contains(gotBody, "128") {
		t.Fatalf("put code=%d forwarded=%q", pw.Code, gotBody)
	}

	// PUT with a bad value is rejected before reaching the device.
	bw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(bw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"BRI":999}`)))
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
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}
	rw := httptest.NewRecorder()
	a.handleDeviceReboot(rw, httptest.NewRequest("POST", "/v1/device/reboot", nil))
	dw := httptest.NewRecorder()
	a.handleDeviceDismiss(dw, httptest.NewRequest("POST", "/v1/device/notify/dismiss", nil))
	if rw.Code != 200 || dw.Code != 200 {
		t.Fatalf("reboot=%d dismiss=%d", rw.Code, dw.Code)
	}
	joined := strings.Join(hits, ",")
	if !strings.Contains(joined, "/api/reboot") || !strings.Contains(joined, "/api/notify/dismiss") {
		t.Fatalf("device endpoints hit: %v", hits)
	}
}

func TestDeviceProxyMapsDeviceErrorTo502(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}
	// GET maps a device 500 to 502.
	gw := httptest.NewRecorder()
	a.handleDeviceSettingsGet(gw, httptest.NewRequest("GET", "/v1/device/settings", nil))
	if gw.Code != http.StatusBadGateway {
		t.Fatalf("get code=%d want 502", gw.Code)
	}
	// PUT (valid body) maps a device 500 to 502.
	pw := httptest.NewRecorder()
	a.handleDeviceSettingsPut(pw, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"BRI":120}`)))
	if pw.Code != http.StatusBadGateway {
		t.Fatalf("put code=%d want 502", pw.Code)
	}
	// Reboot maps a device 500 to 502.
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
