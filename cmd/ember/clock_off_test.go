package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
)

type countingTransport struct {
	n    atomic.Int64
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(r)
}

func TestClockOffMakesNoClockRequests(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "off")

	var stubHits atomic.Int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubHits.Add(1)
		_, _ = w.Write([]byte(`{"uid":"awtrix_x","boardType":"awtrixng"}`))
	}))
	defer stub.Close()
	var ct *countingTransport
	swapDefaultTransport(t, func(next http.RoundTripper) http.RoundTripper {
		ct = &countingTransport{next: next}
		return ct
	})

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = stub.URL
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	app := NewApp(cfg, nil, discardLogger())
	var browses atomic.Int64
	app.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		browses.Add(1)
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	app.initDeviceDiscovery(ctx)
	if app.rediscoverClock(ctx) {
		t.Fatal("rediscover must not swap")
	}
	if got := app.lastRediscoverResult.Load(); got != "disabled" {
		t.Fatalf("rediscover result = %v, want disabled", got)
	}
	app.StartClockSampler(ctx, time.Hour)
	app.ensureBootPingScript(ctx)
	app.RepublishAll("test")
	if err := app.ClearIndicators(ctx); err != nil {
		t.Fatalf("ClearIndicators: %v", err)
	}
	for _, err := range []error{
		app.publisher.CustomApp(ctx, "x", map[string]any{"text": "hi"}),
		app.publisher.Notify(ctx, map[string]any{"text": "hi"}),
		app.publisher.Settings(ctx, map[string]any{"TIME_COL": 1}),
		app.publisher.Switch(ctx, "x", awtrix.SwitchMode(0)),
	} {
		if err != nil {
			t.Fatalf("publisher call: %v", err)
		}
	}
	if app.clock.reachable(ctx, stub.URL) {
		t.Fatal("reachable must be false")
	}
	if _, err := app.clock.readSystem(ctx); err != errClockDisabled {
		t.Fatalf("readSystem err = %v, want errClockDisabled", err)
	}

	h := app.routes()
	for _, rq := range []struct{ method, path, body string }{
		{"GET", "/v1/clock/health", ""},
		{"GET", "/v1/clock/stats?range=15m", ""},
		{"GET", "/v1/device/discover", ""},
		{"GET", "/v1/device/settings", ""},
		{"PUT", "/v1/device/settings", `{}`},
		{"GET", "/v1/device/capabilities", ""},
		{"GET", "/v1/device/sensors", ""},
		{"GET", "/v1/device/screen", ""},
		{"POST", "/v1/device/reboot", ""},
		{"GET", "/v1/device/buttons", ""},
		{"PUT", "/v1/device/buttons", `{}`},
		{"GET", "/v1/device/display", ""},
		{"PUT", "/v1/device/display", `{}`},
		{"PUT", "/v1/device/display/power", `{"power":true}`},
		{"GET", "/v1/device/apps", ""},
		{"PUT", "/v1/device/apps", `{}`},
		{"POST", "/v1/device/audio/test", ""},
		{"POST", "/v1/device/audio/stop", ""},
		{"GET", "/v1/device/audio/melodies", ""},
		{"GET", "/v1/device/stats", ""},
		{"PUT", "/v1/device/sensors", `{}`},
		{"POST", "/v1/device/notify/dismiss", ""},
		{"POST", "/v1/device/app/next", ""},
		{"POST", "/v1/device/app/previous", ""},
		{"PUT", "/v1/device/config", `{"base_url":"http://127.0.0.1:9"}`},
		{"POST", "/hooks/awtrix/boot", ""},
	} {
		req := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body))
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rq.path == "/v1/clock/health" {
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"disabled":true`) || !strings.Contains(rec.Body.String(), `"device":null`) {
				t.Fatalf("health = %d %s", rec.Code, rec.Body)
			}
		}
		if rq.path == "/v1/device/discover" && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("discover = %d, want 503", rec.Code)
		}
	}

	res := runDoctorChecks(ctx, app, app.cfg.Load())
	for _, k := range []string{"clock", "awtrix_reachable", "capabilities"} {
		if c := res.Checks[k]; c.Status != StatusOK || !strings.Contains(c.Detail, "disabled") {
			t.Fatalf("doctor %s = %+v, want ok/disabled", k, c)
		}
	}
	off := runDoctorChecks(ctx, nil, app.cfg.Load())
	if c := off.Checks["awtrix_reachable"]; c.Status != StatusOK || !strings.Contains(c.Detail, "disabled") {
		t.Fatalf("offline doctor awtrix_reachable = %+v", c)
	}
	if ok, fail := app.publishWindow.last24h(time.Now()); ok+fail != 0 {
		t.Fatalf("dropped publishes counted: ok=%d fail=%d", ok, fail)
	}

	if n := stubHits.Load(); n != 0 {
		t.Fatalf("clock stub received %d requests, want 0", n)
	}
	if n := ct.n.Load(); n != 0 {
		t.Fatalf("default transport carried %d requests, want 0", n)
	}
	if n := browses.Load(); n != 0 {
		t.Fatalf("mDNS browse ran %d times, want 0", n)
	}
}

func TestClockOnReachesClock(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "")
	var hits atomic.Int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = stub.URL
	cfg.applyDefaults()
	app := NewApp(cfg, nil, discardLogger())
	app.probeClockHealth(context.Background(), time.Now())
	if hits.Load() == 0 {
		t.Fatal("expected the probe to reach the clock stub")
	}
}

func TestClockDisabledValues(t *testing.T) {
	for v, want := range map[string]bool{"": false, "on": false, "off": true, "OFF": true, " off ": true, "0": true, "false": true, "no": true, "disabled": true, "auto": false} {
		t.Setenv("EMBER_CLOCK", v)
		if got := clockDisabled(); got != want {
			t.Errorf("EMBER_CLOCK=%q: %v, want %v", v, got, want)
		}
	}
}

func TestClockOffDoctorIsOK(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "off")
	app := newAppForDoctor(t, "http://127.0.0.1:9")
	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	if !res.OK {
		t.Fatalf("doctor not OK under EMBER_CLOCK=off: %+v", res.Checks)
	}
}

func TestClockOffSkipsIconProvisioning(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "off")
	app, pub := iconTestApp(t, func(w *WeatherConfig) { w.TileNativeIcons = true })
	fetched := 0
	app.iconFetch = func(context.Context, string) ([]byte, string, error) { fetched++; return nil, "", nil }
	app.ensureNativeIcons(context.Background())
	if fetched != 0 || len(pub.PutIconNamesSnapshot()) != 0 {
		t.Fatalf("fetched %d icons under EMBER_CLOCK=off", fetched)
	}
}
