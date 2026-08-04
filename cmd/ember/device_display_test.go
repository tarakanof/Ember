package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateDeviceDisplay(t *testing.T) {
	ok := []map[string]any{
		{"overlay": nil},
		{"overlay": "rain"},
		{"overlay": "drizzle", "overlaySettings": map[string]any{"speed": float64(2), "palette": "fire", "blend": true}},
		{"overlaySettings": map[string]any{"speed": float64(1), "palette": nil, "blend": false}},
	}
	for i, m := range ok {
		if err := validateDeviceDisplay(m); err != nil {
			t.Fatalf("case %d: valid display body rejected: %v", i, err)
		}
	}
	bad := []map[string]any{
		{"overlay": "rainbow"},          // not in enum
		{"overlay": 3},                  // wrong type
		{"overlay": "clear"},            // NG has no "clear"; use null
		{"power": true},                 // out of scope this ticket
		{"moodlight": map[string]any{}}, // out of scope this ticket
		{"overlaySettings": map[string]any{"speed": "x"}}, // wrong type
		{"overlaySettings": map[string]any{"nope": true}}, // unknown subkey
		{"overlaySettings": "not-an-object"},              // wrong shape
		{"NOPE": true},                                    // unknown top-level key
	}
	for i, m := range bad {
		if err := validateDeviceDisplay(m); err == nil {
			t.Fatalf("case %d: expected rejection for %v", i, m)
		}
	}
}

func TestDeviceDisplayProxy(t *testing.T) {
	var gotMethod, gotBody string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/display" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(200)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"power":true,"brightness":120,"overlay":null,"overlaySettings":{"speed":1,"palette":null,"blend":true},"moodlight":null}`))
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}

	gw := httptest.NewRecorder()
	a.handleDeviceDisplayGet(gw, httptest.NewRequest("GET", "/v1/device/display", nil))
	if gw.Code != 200 || gotMethod != http.MethodGet {
		t.Fatalf("get code=%d method=%s", gw.Code, gotMethod)
	}
	if !strings.Contains(gw.Body.String(), `"overlay":null`) {
		t.Fatalf("get body=%s", gw.Body.String())
	}

	pw := httptest.NewRecorder()
	a.handleDeviceDisplayPut(pw, httptest.NewRequest("PUT", "/v1/device/display", strings.NewReader(`{"overlay":"snow"}`)))
	if pw.Code != 200 || gotMethod != http.MethodPatch || !strings.Contains(gotBody, "snow") {
		t.Fatalf("put code=%d method=%s forwarded=%q", pw.Code, gotMethod, gotBody)
	}

	bw := httptest.NewRecorder()
	a.handleDeviceDisplayPut(bw, httptest.NewRequest("PUT", "/v1/device/display", strings.NewReader(`{"overlay":"rainbow"}`)))
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("bad put code=%d want 400", bw.Code)
	}
}

func TestDeviceAppsProxy(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		switch r.URL.Path {
		case "/api/v1/apps":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"Time","enabled":true,"inLoop":true}]`))
		case "/api/v1/apps/order":
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(200)
		default:
			http.NotFound(w, r)
		}
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}

	gw := httptest.NewRecorder()
	a.handleDeviceAppsGet(gw, httptest.NewRequest("GET", "/v1/device/apps", nil))
	if gw.Code != 200 || gotPath != "/api/v1/apps" {
		t.Fatalf("get code=%d path=%s", gw.Code, gotPath)
	}
	if !strings.Contains(gw.Body.String(), `"Time"`) {
		t.Fatalf("get body=%s", gw.Body.String())
	}

	pw := httptest.NewRecorder()
	a.handleDeviceAppsPut(pw, httptest.NewRequest("PUT", "/v1/device/apps",
		strings.NewReader(`{"order":["Time","Date"],"disabled":["Battery"]}`)))
	if pw.Code != 200 || gotMethod != http.MethodPut || gotPath != "/api/v1/apps/order" {
		t.Fatalf("put code=%d method=%s path=%s", pw.Code, gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, "Battery") {
		t.Fatalf("forwarded=%q", gotBody)
	}

	bad := []string{
		`{"order":[1,2]}`,                 // non-string entries
		`{"order":["Time"],"extra":true}`, // unknown top-level key
		`{"foo":"bar"}`,                   // completely wrong shape
	}
	for _, body := range bad {
		bw := httptest.NewRecorder()
		a.handleDeviceAppsPut(bw, httptest.NewRequest("PUT", "/v1/device/apps", strings.NewReader(body)))
		if bw.Code != http.StatusBadRequest {
			t.Fatalf("body=%q code=%d want 400", body, bw.Code)
		}
	}
}

func TestDeviceDisplayAppsMapDeviceErrorTo502(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dev.Close()
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewRecorder()
	a.handleDeviceDisplayGet(gw, httptest.NewRequest("GET", "/v1/device/display", nil))
	if gw.Code != http.StatusBadGateway {
		t.Fatalf("display get code=%d want 502", gw.Code)
	}
	aw := httptest.NewRecorder()
	a.handleDeviceAppsGet(aw, httptest.NewRequest("GET", "/v1/device/apps", nil))
	if aw.Code != http.StatusBadGateway {
		t.Fatalf("apps get code=%d want 502", aw.Code)
	}
}
