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

// Test budgets, scaled down from clockWriteBudget (25s) under the server's
// WriteTimeout (30s). The gap is wide so a slow -race run can't flake it.
const (
	testWriteBudget  = 200 * time.Millisecond
	testReadBudget   = 200 * time.Millisecond
	testWriteTimeout = 2 * time.Second
)

// stallClock is a fake awtrix-ng clock whose system and settings calls can
// stall until the caller gives up, and which records what reached it.
type stallClock struct {
	mu            sync.Mutex
	system        map[string]any
	stallGets     map[int]bool // stall the nth GET /api/v1/system (1-based)
	stallPut      bool         // stall PUT /api/v1/system and PATCH /api/v1/settings
	stallSettings bool         // stall GET /api/v1/settings
	onWrite       func()       // runs before a write is answered
	onSettingsGet func()       // runs before GET /api/v1/settings is answered
	gets          int          // GET /api/v1/system
	settingsGets  int          // GET /api/v1/settings
	writes        int
	release       chan struct{}
}

func newStallClock(t *testing.T) (*stallClock, *httptest.Server) {
	t.Helper()
	f := &stallClock{
		system:    map[string]any{"tempOffset": -9.0, "humOffset": 0.0, "buttonCallback": "", "wifiSsid": "home"},
		stallGets: map[int]bool{},
		release:   make(chan struct{}),
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(f.release) }) // runs first: frees stalled handlers
	return f, srv
}

func (f *stallClock) stall(r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-f.release:
	}
}

func (f *stallClock) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	var stall bool
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/settings":
		f.settingsGets++
		stallRead, onRead := f.stallSettings, f.onSettingsGet
		f.mu.Unlock()
		if onRead != nil {
			onRead()
		}
		if stallRead {
			f.stall(r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"brightness":64,"autoTransition":false}`))
		return
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/system":
		f.gets++
		stall = f.stallGets[f.gets]
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/system",
		r.Method == http.MethodPatch && r.URL.Path == "/api/v1/settings":
		f.writes++
		stall = f.stallPut
		if !stall && r.URL.Path == "/api/v1/system" {
			_ = json.Unmarshal(body, &f.system)
		}
	default:
		f.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	sys, _ := json.Marshal(f.system)
	onWrite := f.onWrite
	f.mu.Unlock()
	if onWrite != nil && r.Method != http.MethodGet {
		onWrite()
	}
	if stall {
		f.stall(r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_, _ = w.Write(sys)
	}
}

func (f *stallClock) settingsGetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settingsGets
}

func (f *stallClock) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

// budgetTestApp is an App on clock with the write budget shortened.
func budgetTestApp(t *testing.T, clock string) *App {
	t.Helper()
	a := sensorTestApp(t, clock)
	a.clock.writeBudget = testWriteBudget
	a.clock.readBudget = testReadBudget
	return a
}

// serveOnce sends one request to h behind a real http.Server with a
// WriteTimeout, the way main.go serves it, and returns the answer. A handler
// that ran past the WriteTimeout would fail here with a transport error.
func serveOnce(t *testing.T, h http.HandlerFunc, method, path, body string) (int, map[string]any, time.Duration) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = testWriteTimeout
	srv.Start()
	defer srv.Close()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: connection lost instead of an answer: %v", method, path, err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s %s: status %d, undecodable body: %v", method, path, resp.StatusCode, err)
	}
	return resp.StatusCode, out, elapsed
}

// A chained clock write that runs out of its budget answers 504 in time, in
// the clock error shape, saying whether the write may have landed.
func TestClockWriteBudgetAnswers504BeforeWriteTimeout(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(a *App, f *stallClock)
		handler    func(a *App) http.HandlerFunc
		path, body string
		wantWrite  string
		wantWrites int // requests that reached the clock's write endpoint
	}{
		{
			name:    "sensors behind a stuck systemLock",
			setup:   func(a *App, _ *stallClock) { a.clock.systemLock.Lock() },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSensorsPut },
			path:    "/v1/device/sensors", body: `{"temp_offset":-7}`,
			wantWrite: "not_sent",
		},
		{
			name:    "sensors read stalls",
			setup:   func(_ *App, f *stallClock) { f.stallGets[1] = true },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSensorsPut },
			path:    "/v1/device/sensors", body: `{"temp_offset":-7}`,
			wantWrite: "not_sent",
		},
		{
			name:    "sensors PUT stalls",
			setup:   func(_ *App, f *stallClock) { f.stallPut = true },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSensorsPut },
			path:    "/v1/device/sensors", body: `{"temp_offset":-7}`,
			wantWrite: "unknown", wantWrites: 1,
		},
		{
			name:    "buttons PUT stalls",
			setup:   func(_ *App, f *stallClock) { f.stallPut = true },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceButtonsPut },
			path:    "/v1/device/buttons", body: `{"enabled":true}`,
			wantWrite: "unknown", wantWrites: 1,
		},
		{
			name:    "settings PATCH stalls",
			setup:   func(_ *App, f *stallClock) { f.stallPut = true },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSettingsPut },
			path:    "/v1/device/settings", body: `{"brightness":64}`,
			wantWrite: "unknown", wantWrites: 1,
		},
		{
			name: "settings behind a stuck priorMu",
			setup: func(a *App, _ *stallClock) {
				startTestTakeover(a, takeoverPrior{AutoTransition: true})
				a.coord.priorMu.Lock()
			},
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSettingsPut },
			path:    "/v1/device/settings", body: `{"autoTransition":false,"brightness":64}`,
			wantWrite: "not_sent",
		},
		{
			// A takeover edge takes priorMu while the unlocked edit's PATCH
			// is in flight and keeps it past the budget.
			name:    "settings PATCH landed, reconcile behind a stuck priorMu",
			setup:   func(a *App, f *stallClock) { f.onWrite = func() { a.coord.priorMu.Lock() } },
			handler: func(a *App) http.HandlerFunc { return a.handleDeviceSettingsPut },
			path:    "/v1/device/settings", body: `{"autoTransition":false}`,
			wantWrite: "applied", wantWrites: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, clock := newStallClock(t)
			a := budgetTestApp(t, clock.URL)
			c.setup(a, f)

			status, body, elapsed := serveOnce(t, c.handler(a), http.MethodPut, c.path, c.body)
			if status != http.StatusGatewayTimeout {
				t.Fatalf("status = %d body = %v, want 504", status, body)
			}
			if elapsed >= testWriteTimeout {
				t.Fatalf("answered after %s, past the %s WriteTimeout", elapsed, testWriteTimeout)
			}
			if body["code"] != "clock_timeout" || body["write"] != c.wantWrite {
				t.Fatalf("body = %v, want code clock_timeout, write %s", body, c.wantWrite)
			}
			if msg, _ := body["error"].(string); !strings.Contains(msg, testWriteBudget.String()) {
				t.Fatalf("error = %q, want it to name the %s budget", msg, testWriteBudget)
			}
			if got := f.writeCount(); got != c.wantWrites {
				t.Fatalf("clock saw %d writes, want %d", got, c.wantWrites)
			}
		})
	}
}

// A system write that landed but whose re-read ran out of budget answers
// from the object it wrote, not with an error.
func TestClockWriteBudgetAfterLandedPutAnswersWrittenObject(t *testing.T) {
	f, clock := newStallClock(t)
	// Each PUT reads (GETs 1 and 3), writes, then re-reads (GETs 2 and 4).
	f.stallGets[2], f.stallGets[4] = true, true
	a := budgetTestApp(t, clock.URL)

	status, body, _ := serveOnce(t, a.handleDeviceSensorsPut, http.MethodPut, "/v1/device/sensors", `{"temp_offset":-7}`)
	if status != http.StatusOK || body["temp_offset"] != -7.0 || body["hum_offset"] != 0.0 {
		t.Fatalf("sensors: status = %d body = %v, want 200 with the written offsets", status, body)
	}

	status, body, _ = serveOnce(t, a.handleDeviceButtonsPut, http.MethodPut, "/v1/device/buttons", `{"enabled":true}`)
	if status != http.StatusOK || body["configured"] != true {
		t.Fatalf("buttons: status = %d body = %v, want 200 with the written callback", status, body)
	}
}

// GET /v1/device/settings waits on priorMu around its clock read. Behind a
// stalled holder, or with the read itself stalled, it answers 504 in time in
// the clock error shape, with no "write" field: a read changes nothing.
func TestClockReadBudgetSettingsGetAnswers504BeforeWriteTimeout(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *App, f *stallClock)
		reads int // GET /api/v1/settings that reached the clock
	}{
		{
			name: "first priorMu wait behind a stalled holder",
			setup: func(a *App, _ *stallClock) {
				startTestTakeover(a, takeoverPrior{AutoTransition: true})
				a.coord.priorMu.Lock()
			},
		},
		{
			name:  "settings read stalls",
			setup: func(_ *App, f *stallClock) { f.stallSettings = true },
			reads: 1,
		},
		{
			// A takeover edge or menu edit takes priorMu while the read is
			// in flight and keeps it past the budget.
			name: "second priorMu wait behind a stalled holder",
			setup: func(a *App, f *stallClock) {
				startTestTakeover(a, takeoverPrior{AutoTransition: true})
				f.onSettingsGet = func() { a.coord.priorMu.Lock() }
			},
			reads: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, clock := newStallClock(t)
			a := budgetTestApp(t, clock.URL)
			c.setup(a, f)

			status, body, elapsed := serveOnce(t, a.handleDeviceSettingsGet, http.MethodGet, "/v1/device/settings", "")
			if status != http.StatusGatewayTimeout {
				t.Fatalf("status = %d body = %v, want 504", status, body)
			}
			if elapsed >= testWriteTimeout {
				t.Fatalf("answered after %s, past the %s WriteTimeout", elapsed, testWriteTimeout)
			}
			if body["code"] != "clock_timeout" {
				t.Fatalf("body = %v, want code clock_timeout", body)
			}
			if _, ok := body["write"]; ok {
				t.Fatalf("body = %v, want no write field on a read", body)
			}
			if msg, _ := body["error"].(string); !strings.Contains(msg, testReadBudget.String()) || strings.Contains(msg, ":") {
				t.Fatalf("error = %q, want it to name the %s budget with no write fate", msg, testReadBudget)
			}
			if got := f.writeCount(); got != 0 {
				t.Fatalf("clock saw %d writes, want 0", got)
			}
			if got := f.settingsGetCount(); got != c.reads {
				t.Fatalf("clock saw %d settings reads, want %d", got, c.reads)
			}
		})
	}
}
