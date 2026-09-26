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

// fakeClockCall is one request the fake clock received.
type fakeClockCall struct {
	method, path, body string
}

// fakeAudioClock serves reply for every request and records the calls.
func fakeAudioClock(t *testing.T, status int, reply string) (*App, func() []fakeClockCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []fakeClockCall
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, fakeClockCall{r.Method, r.URL.Path, string(b)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(dev.Close)
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL(dev.URL); err != nil {
		t.Fatal(err)
	}
	return a, func() []fakeClockCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]fakeClockCall(nil), calls...)
	}
}

func withAudioCaps(a *App, audio awtrix.AudioCaps) {
	a.caps.Store(&awtrix.Capabilities{Audio: audio})
}

func TestDevicePowerPutForwardsPowerOnly(t *testing.T) {
	for _, on := range []bool{false, true} {
		a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
		body := `{"power":false}`
		if on {
			body = `{"power":true}`
		}
		w := httptest.NewRecorder()
		a.handleDevicePowerPut(w, httptest.NewRequest("PUT", "/v1/device/display/power", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		got := calls()
		if len(got) != 1 || got[0].method != http.MethodPatch || got[0].path != "/api/v1/display" {
			t.Fatalf("calls = %+v", got)
		}
		var sent map[string]any
		if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil || sent["power"] != on || len(sent) != 1 {
			t.Fatalf("forwarded %q", got[0].body)
		}
	}
}

func TestDevicePowerPutRejectsBadBodyWithoutCallingClock(t *testing.T) {
	for _, body := range []string{`{}`, `{"power":"off"}`, `{"power":null}`, `{"power":true,"overlay":"rain"}`, `not json`} {
		a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
		w := httptest.NewRecorder()
		a.handleDevicePowerPut(w, httptest.NewRequest("PUT", "/v1/device/display/power", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code=%d want 400", body, w.Code)
		}
		if n := len(calls()); n != 0 {
			t.Fatalf("%s: clock called %d times", body, n)
		}
	}
}

// A device refusal reaches the menu through the same envelope relay as every
// other /v1/device proxy: status, code and field intact.
func TestDevicePowerPutRelaysClockError(t *testing.T) {
	a, _ := fakeAudioClock(t, http.StatusUnprocessableEntity,
		`{"error":{"code":"validationFailed","message":"must be a boolean","field":"power"}}`)
	w := httptest.NewRecorder()
	a.handleDevicePowerPut(w, httptest.NewRequest("PUT", "/v1/device/display/power", strings.NewReader(`{"power":true}`)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	m := decodeProxyError(t, w)
	if m["field"] != "power" || m["code"] != "validationFailed" {
		t.Fatalf("body = %v", m)
	}
}

func TestDevicePowerPutUnreachableClockIs502(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL("http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleDevicePowerPut(w, httptest.NewRequest("PUT", "/v1/device/display/power", strings.NewReader(`{"power":true}`)))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code=%d want 502", w.Code)
	}
}

func TestDeviceAudioTestPlaysBuiltInChime(t *testing.T) {
	a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	w := httptest.NewRecorder()
	a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	got := calls()
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != "/api/v1/audio/play" {
		t.Fatalf("calls = %+v", got)
	}
	if !strings.Contains(got[0].body, `"rtttl":"`+testChimeRTTTL+`"`) {
		t.Fatalf("forwarded %q", got[0].body)
	}
}

func TestDeviceAudioTestPlaysNamedMelody(t *testing.T) {
	a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	w := httptest.NewRecorder()
	a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", strings.NewReader(`{"melody":"door_bell-2"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	got := calls()
	if len(got) != 1 || got[0].body != `{"melody":"door_bell-2"}` {
		t.Fatalf("calls = %+v", got)
	}
}

func TestDeviceAudioTestRejectsBadMelodyName(t *testing.T) {
	for _, body := range []string{`{"melody":""}`, `{"melody":"has space"}`, `{"melody":"` + strings.Repeat("a", 25) + `"}`, `{"melody":3}`, `{"sound":"x"}`} {
		a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
		withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
		w := httptest.NewRecorder()
		a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || len(calls()) != 0 {
			t.Fatalf("%s: code=%d calls=%d", body, w.Code, len(calls()))
		}
	}
}

// A clock without a buzzer is refused up front with the same 503 the firmware
// itself would give, so the menu can hide the button on either signal.
func TestDeviceAudioRoutesGatedOnBuzzer(t *testing.T) {
	cases := []struct {
		name string
		call func(a *App, w http.ResponseWriter)
	}{
		{"test", func(a *App, w http.ResponseWriter) {
			a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", nil))
		}},
		{"melodies", func(a *App, w http.ResponseWriter) {
			a.handleDeviceAudioMelodies(w, httptest.NewRequest("GET", "/v1/device/audio/melodies", nil))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
			withAudioCaps(a, awtrix.AudioCaps{MP3: true, Radio: true})
			w := httptest.NewRecorder()
			c.call(a, w)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("code=%d want 503", w.Code)
			}
			if m := decodeProxyError(t, w); m["code"] != "unavailable" {
				t.Fatalf("body = %v", m)
			}
			if n := len(calls()); n != 0 {
				t.Fatalf("clock called %d times", n)
			}
		})
	}
}

func TestDeviceAudioStopGatedOnAnyOutput(t *testing.T) {
	a, calls := fakeAudioClock(t, http.StatusOK, `{"ok":true}`)
	withAudioCaps(a, awtrix.AudioCaps{})
	w := httptest.NewRecorder()
	a.handleDeviceAudioStop(w, httptest.NewRequest("POST", "/v1/device/audio/stop", nil))
	if w.Code != http.StatusServiceUnavailable || len(calls()) != 0 {
		t.Fatalf("no outputs: code=%d calls=%d", w.Code, len(calls()))
	}

	withAudioCaps(a, awtrix.AudioCaps{Radio: true})
	w = httptest.NewRecorder()
	a.handleDeviceAudioStop(w, httptest.NewRequest("POST", "/v1/device/audio/stop", nil))
	got := calls()
	if w.Code != http.StatusOK || len(got) != 1 || got[0].path != "/api/v1/audio/stop" || got[0].body != "" {
		t.Fatalf("radio only: code=%d calls=%+v", w.Code, got)
	}
}

// With nothing cached (clock dark at boot) the request goes through and the
// clock's own answer decides — a 503 from it is relayed as-is.
func TestDeviceAudioColdCapsDefersToClock(t *testing.T) {
	a, calls := fakeAudioClock(t, http.StatusServiceUnavailable,
		`{"error":{"code":"unavailable","message":"no buzzer"}}`)
	w := httptest.NewRecorder()
	a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", nil))
	if w.Code != http.StatusServiceUnavailable || len(calls()) != 1 {
		t.Fatalf("code=%d calls=%d", w.Code, len(calls()))
	}
	if m := decodeProxyError(t, w); m["code"] != "unavailable" {
		t.Fatalf("body = %v", m)
	}
}

func TestDeviceAudioMelodiesListsClockMelodies(t *testing.T) {
	a, calls := fakeAudioClock(t, http.StatusOK, `{"melodies":[
		{"name":"doorbell","rtttl":"doorbell:d=4,o=5,b=100:e,c","bytes":26,"notes":2,"durationMs":2400,"valid":true}],
		"usedBytes":41216,"totalBytes":1048576}`)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	w := httptest.NewRecorder()
	a.handleDeviceAudioMelodies(w, httptest.NewRequest("GET", "/v1/device/audio/melodies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if got := calls(); len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/api/v1/audio/melodies" {
		t.Fatalf("calls = %+v", got)
	}
	var out awtrix.MelodyList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Melodies) != 1 || out.Melodies[0].Name != "doorbell" || out.Melodies[0].DurationMs != 2400 ||
		out.UsedBytes != 41216 || out.TotalBytes != 1048576 {
		t.Fatalf("out = %+v", out)
	}
}

func TestDeviceAudioMelodiesRelaysClockError(t *testing.T) {
	a, _ := fakeAudioClock(t, http.StatusNotFound, `{"error":{"code":"notFound","message":"no such route"}}`)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	w := httptest.NewRecorder()
	a.handleDeviceAudioMelodies(w, httptest.NewRequest("GET", "/v1/device/audio/melodies", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", w.Code)
	}
	if m := decodeProxyError(t, w); m["code"] != "notFound" {
		t.Fatalf("body = %v", m)
	}
}

// The new routes are writes to the clock and sit behind bearer auth, the
// melody list included (it is part of the /v1/device group).
func TestDevicePowerAudioRoutesRequireAuth(t *testing.T) {
	a := newTestAppWithStore(t)
	cur := *a.cfg.Load()
	cur.Auth.StatusToken = "secret"
	a.cfg.Store(&cur)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	routes := []struct{ method, path, body string }{
		{"PUT", "/v1/device/display/power", `{"power":true}`},
		{"POST", "/v1/device/audio/test", ""},
		{"POST", "/v1/device/audio/stop", ""},
		{"GET", "/v1/device/audio/melodies", ""},
	}
	for _, r := range routes {
		req, _ := http.NewRequest(r.method, srv.URL+r.path, strings.NewReader(r.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without token: %d, want 401", r.method, r.path, resp.StatusCode)
		}
	}
}
