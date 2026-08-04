package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildCallbackURL(t *testing.T) {
	cases := []struct {
		ip, addr, want string
	}{
		{"192.168.0.2", ":3627", "http://192.168.0.2:3627/hooks/awtrix/button"},
		{"10.0.0.5", "0.0.0.0:3627", "http://10.0.0.5:3627/hooks/awtrix/button"},
		{"", ":3627", ""},           // no IP → no URL
		{"192.168.0.2", "", ""},     // no addr → no URL
		{"192.168.0.2", "junk", ""}, // unparseable addr → no URL
	}
	for _, c := range cases {
		if got := buildCallbackURL(c.ip, c.addr); got != c.want {
			t.Fatalf("buildCallbackURL(%q,%q)=%q want %q", c.ip, c.addr, got, c.want)
		}
	}
}

func TestButtonPressTrackedAndReported(t *testing.T) {
	dev := (&fakeSystemDevice{system: map[string]any{"buttonCallback": ""}}).server(t)
	defer dev.Close()
	a := sensorTestApp(t, dev.URL)

	// Fresh app: no presses yet → seconds_since null.
	w := httptest.NewRecorder()
	a.handleDeviceButtons(w, httptest.NewRequest("GET", "/v1/device/buttons", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"seconds_since":null`) {
		t.Fatalf("fresh app should report null seconds_since, got %s", w.Body.String())
	}

	// A received button POST is recorded even when Pomodoro is disabled.
	pr := httptest.NewRequest("POST", "/hooks/awtrix/button", strings.NewReader("button=select&state=1"))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handleAwtrixButton(httptest.NewRecorder(), pr)

	w2 := httptest.NewRecorder()
	a.handleDeviceButtons(w2, httptest.NewRequest("GET", "/v1/device/buttons", nil))
	body := w2.Body.String()
	if strings.Contains(body, `"seconds_since":null`) {
		t.Fatalf("after a press, seconds_since should be set, got %s", body)
	}
	if !strings.Contains(body, `"last_press_unix"`) {
		t.Fatalf("missing last_press_unix: %s", body)
	}
}

func TestDeviceButtonsGetReportsConfiguredVsExpected(t *testing.T) {
	expected := "http://192.168.0.2:3627/hooks/awtrix/button"
	dev := (&fakeSystemDevice{system: map[string]any{"buttonCallback": expected}}).server(t)
	defer dev.Close()
	a := sensorTestApp(t, dev.URL)

	w := httptest.NewRecorder()
	a.handleDeviceButtons(w, httptest.NewRequest("GET", "/v1/device/buttons", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ConfiguredCallback string `json:"configured_callback"`
		Configured         bool   `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ConfiguredCallback != expected {
		t.Fatalf("configured_callback=%q want %q", got.ConfiguredCallback, expected)
	}
}

func TestDeviceButtonsPutSetsAndClearsCallback(t *testing.T) {
	dev := &fakeSystemDevice{system: map[string]any{"buttonCallback": "", "wifiSsid": "keepme"}}
	srv := dev.server(t)
	defer srv.Close()
	a := sensorTestApp(t, srv.URL)

	w := httptest.NewRecorder()
	a.handleDeviceButtonsPut(w, httptest.NewRequest("PUT", "/v1/device/buttons", strings.NewReader(`{"enabled":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("enable code=%d body=%s", w.Code, w.Body.String())
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(dev.lastPut), &merged); err != nil {
		t.Fatal(err)
	}
	cb, _ := merged["buttonCallback"].(string)
	if cb == "" {
		t.Fatalf("buttonCallback not set: %v", merged)
	}
	if merged["wifiSsid"] != "keepme" {
		t.Fatalf("unrelated system keys must survive: %v", merged)
	}

	w2 := httptest.NewRecorder()
	a.handleDeviceButtonsPut(w2, httptest.NewRequest("PUT", "/v1/device/buttons", strings.NewReader(`{"enabled":false}`)))
	if w2.Code != http.StatusOK {
		t.Fatalf("disable code=%d body=%s", w2.Code, w2.Body.String())
	}
	var merged2 map[string]any
	if err := json.Unmarshal([]byte(dev.lastPut), &merged2); err != nil {
		t.Fatal(err)
	}
	if got := merged2["buttonCallback"]; got != "" {
		t.Fatalf("buttonCallback=%v want cleared", got)
	}
}

func TestDeviceButtonsPutRejectsBadBody(t *testing.T) {
	dev := (&fakeSystemDevice{system: map[string]any{}}).server(t)
	defer dev.Close()
	a := sensorTestApp(t, dev.URL)

	w := httptest.NewRecorder()
	a.handleDeviceButtonsPut(w, httptest.NewRequest("PUT", "/v1/device/buttons", strings.NewReader(`{"enabled":"yes"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", w.Code)
	}
}
