package main

import (
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
	a := newTestAppWithStore(t)

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
