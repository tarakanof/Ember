package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A clock refusal of the system write (NG 422 with its envelope) reaches the
// menu as the same relay every other /v1/device route gives it, not as a bare
// 502 that hides which key the firmware refused (#146).
func TestDeviceSystemWritesRelayClockRefusal(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"tempOffset":-9,"humOffset":0,"buttonCallback":""}`)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":{"code":"validationFailed","message":"out of range","field":"tempOffset"}}`)
	}))
	defer dev.Close()
	a := sensorTestApp(t, dev.URL)

	for name, run := range map[string]func(w http.ResponseWriter){
		"sensors": func(w http.ResponseWriter) {
			a.handleDeviceSensorsPut(w, httptest.NewRequest("PUT", "/v1/device/sensors", strings.NewReader(`{"temp_offset":3}`)))
		},
		"buttons": func(w http.ResponseWriter) {
			a.handleDeviceButtonsPut(w, httptest.NewRequest("PUT", "/v1/device/buttons", strings.NewReader(`{"enabled":true}`)))
		},
	} {
		w := httptest.NewRecorder()
		run(w)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: code=%d want 422 (body %s)", name, w.Code, w.Body.String())
		}
		if m := decodeProxyError(t, w); m["field"] != "tempOffset" || m["code"] != "validationFailed" {
			t.Fatalf("%s: body = %v", name, m)
		}
	}
}

// A refused system read is relayed the same way on the sensors GET.
func TestDeviceSensorsGetRelaysClockRefusal(t *testing.T) {
	a := ngRejecting(t, http.StatusNotFound, "notFound", "")
	w := httptest.NewRecorder()
	a.handleDeviceSensorsGet(w, httptest.NewRequest("GET", "/v1/device/sensors", nil))
	if w.Code != http.StatusNotFound || decodeProxyError(t, w)["code"] != "notFound" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

// Two menu writes to /api/v1/system at once (a sensor offset and the button
// callback) are each a full read-merge-PUT. Unserialised, both read the same
// object and the second PUT silently drops the first one's change (#146).
func TestDeviceSystemWritesAreSerialised(t *testing.T) {
	var mu sync.Mutex
	system := map[string]any{"tempOffset": -9.0, "humOffset": 0.0, "buttonCallback": "", "wifiPassword": "secret"}
	gets := 0
	secondGet := make(chan struct{})
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Hold the first read open until a second read arrives (or 300ms
			// pass): unserialised writers always interleave here.
			mu.Lock()
			gets++
			if gets == 2 {
				close(secondGet)
			}
			mu.Unlock()
			select {
			case <-secondGet:
			case <-time.After(300 * time.Millisecond):
			}
			mu.Lock()
			b, _ := json.Marshal(system)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case http.MethodPut:
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			mu.Lock()
			system = m
			mu.Unlock()
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer dev.Close()
	a := sensorTestApp(t, dev.URL)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a.handleDeviceSensorsPut(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/device/sensors", strings.NewReader(`{"temp_offset":-4}`)))
	}()
	go func() {
		defer wg.Done()
		a.handleDeviceButtonsPut(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/device/buttons", strings.NewReader(`{"enabled":true}`)))
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if system["tempOffset"] != -4.0 {
		t.Errorf("tempOffset = %v, want -4 (sensor write lost)", system["tempOffset"])
	}
	if cb, _ := system["buttonCallback"].(string); cb == "" {
		t.Errorf("buttonCallback empty (button write lost); system = %v", system)
	}
	if system["wifiPassword"] != "secret" {
		t.Errorf("wifiPassword = %v, want preserved", system["wifiPassword"])
	}
}
