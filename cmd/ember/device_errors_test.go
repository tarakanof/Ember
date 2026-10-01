package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func ngRejecting(t *testing.T, status int, code, field string) *App {
	t.Helper()
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"unknown field","field":"`+field+`"}}`)
	}))
	t.Cleanup(dev.Close)
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, dev.URL); err != nil {
		t.Fatal(err)
	}
	return a
}

func decodeProxyError(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, w.Body.String())
	}
	return m
}

func TestDeviceSettingsPutForwardsNGValidationError(t *testing.T) {
	a := ngRejecting(t, http.StatusUnprocessableEntity, "validationFailed", "brightness")
	w := httptest.NewRecorder()
	a.handleDeviceSettingsPut(w, httptest.NewRequest("PUT", "/v1/device/settings", strings.NewReader(`{"brightness":50}`)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d want 422 (body %s)", w.Code, w.Body.String())
	}
	m := decodeProxyError(t, w)
	if m["field"] != "brightness" || m["code"] != "validationFailed" {
		t.Fatalf("body = %v", m)
	}
	if msg, _ := m["error"].(string); !strings.Contains(msg, "brightness") || !strings.Contains(msg, "unknown field") {
		t.Fatalf("error message %q must name the field and the reason", msg)
	}
}

func TestDeviceProxyErrorStatusMapping(t *testing.T) {
	cases := []struct{ device, want int }{
		{http.StatusBadRequest, http.StatusBadRequest},
		{http.StatusNotFound, http.StatusNotFound},
		{http.StatusUnsupportedMediaType, http.StatusUnsupportedMediaType},
		{http.StatusUnprocessableEntity, http.StatusUnprocessableEntity},
		{http.StatusConflict, http.StatusConflict},
		{http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge},
		{http.StatusServiceUnavailable, http.StatusServiceUnavailable},
		{http.StatusTooManyRequests, http.StatusBadGateway},
		{http.StatusUnauthorized, http.StatusBadGateway},
		{http.StatusForbidden, http.StatusBadGateway},
		{http.StatusInternalServerError, http.StatusBadGateway},
		{http.StatusInsufficientStorage, http.StatusBadGateway},
	}
	for _, c := range cases {
		if got := deviceProxyStatus(c.device); got != c.want {
			t.Errorf("device %d → %d, want %d", c.device, got, c.want)
		}
	}
}

func TestWriteDeviceErrorTruncatesOnRuneBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	writeDeviceError(w, http.StatusInternalServerError, []byte(strings.Repeat("é", 300)))
	msg, _ := decodeProxyError(t, w)["error"].(string)
	if !utf8.ValidString(msg) {
		t.Fatalf("truncated message is not valid UTF-8: %q", msg)
	}
	if want := "clock returned 500: " + strings.Repeat("é", 200) + "…"; msg != want {
		t.Fatalf("msg = %q, want 200 runes plus ellipsis", msg)
	}
}

func TestDeviceActionForwardsNGError(t *testing.T) {
	a := ngRejecting(t, http.StatusServiceUnavailable, "serviceBusy", "")
	w := httptest.NewRecorder()
	a.handleDeviceNextApp(w, httptest.NewRequest("POST", "/v1/device/app/next", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d want 503", w.Code)
	}
	if m := decodeProxyError(t, w); m["code"] != "serviceBusy" {
		t.Fatalf("body = %v", m)
	}
}

func TestDeviceDisplayAndAppsPutForwardNGError(t *testing.T) {
	a := ngRejecting(t, http.StatusUnprocessableEntity, "validationFailed", "overlay")
	dw := httptest.NewRecorder()
	a.handleDeviceDisplayPut(dw, httptest.NewRequest("PUT", "/v1/device/display", strings.NewReader(`{"overlay":"rain"}`)))
	if dw.Code != http.StatusUnprocessableEntity || decodeProxyError(t, dw)["field"] != "overlay" {
		t.Fatalf("display put code=%d body=%s", dw.Code, dw.Body.String())
	}
	aw := httptest.NewRecorder()
	a.handleDeviceAppsPut(aw, httptest.NewRequest("PUT", "/v1/device/apps", strings.NewReader(`{"disabled":["Time"]}`)))
	if aw.Code != http.StatusUnprocessableEntity || decodeProxyError(t, aw)["code"] != "validationFailed" {
		t.Fatalf("apps put code=%d body=%s", aw.Code, aw.Body.String())
	}
}

func TestDeviceCapabilitiesLiveFetchForwardsNGError(t *testing.T) {
	a := ngRejecting(t, http.StatusNotFound, "notFound", "")
	w := httptest.NewRecorder()
	a.handleDeviceCapabilities(w, httptest.NewRequest("GET", "/v1/device/capabilities", nil))
	if w.Code != http.StatusNotFound || decodeProxyError(t, w)["code"] != "notFound" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
