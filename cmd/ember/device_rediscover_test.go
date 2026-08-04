package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

// newTestApp builds a bare App (no store) with a config and a discard logger,
// suitable for rediscoverClock tests that don't need store persistence.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp(defaultConfig(), &recordingPublisher{}, discardLogger())
	return a
}

func TestRediscoverClock_SwapsWhenCurrentUnreachable(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`)) // /api/v1/device fingerprint
	}))
	defer clock.Close()
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" }) // unreachable
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}
	changed := a.rediscoverClock(context.Background())
	if !changed || a.cfg.Load().AWTRIX.HTTPBaseURL != clock.URL {
		t.Fatalf("expected swap to %s, got changed=%v url=%s", clock.URL, changed, a.cfg.Load().AWTRIX.HTTPBaseURL)
	}
	if got := a.lastRediscoverResult.Load(); got != "swapped" {
		t.Fatalf("lastRediscoverResult=%v want swapped", got)
	}
	if a.lastRediscoverAt.Load() == 0 {
		t.Fatalf("lastRediscoverAt not recorded")
	}
	if !a.deviceAutoPicked {
		t.Fatalf("expected deviceAutoPicked=true after swap")
	}
}

func TestRediscoverClock_NoopWhenReachable(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"x","boardType":"awtrixng"}`))
	}))
	defer clock.Close()
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	browsed := false
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { browsed = true; return nil, nil }
	if a.rediscoverClock(context.Background()) || browsed {
		t.Fatalf("reachable clock must be a no-op with no browse")
	}
	if got := a.lastRediscoverResult.Load(); got != "reachable" {
		t.Fatalf("lastRediscoverResult=%v want reachable", got)
	}
}

func TestRediscoverClock_NoDeviceFound(t *testing.T) {
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" }) // unreachable
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return nil, nil
	}
	if a.rediscoverClock(context.Background()) {
		t.Fatalf("expected no swap when browse finds nothing")
	}
	if got := a.lastRediscoverResult.Load(); got != "no-device" {
		t.Fatalf("lastRediscoverResult=%v want no-device", got)
	}
}

// TestInitDeviceDiscovery_FallsThroughUnreachableStoreOverride covers the
// boot fall-through fix: previously initDeviceDiscovery returned early once
// deviceSource() == "store" (a menu-chosen override existed), even if that
// override was unreachable. Now the reachability check + browse happen
// regardless of source.
func TestInitDeviceDiscovery_FallsThroughUnreachableStoreOverride(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "http://127.0.0.1:9"); err != nil { // stale store override
		t.Fatal(err)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	a.initDeviceDiscovery(context.Background())

	if got := a.cfg.Load().AWTRIX.HTTPBaseURL; got != clock.URL {
		t.Fatalf("expected fall-through to discovered clock %s, got %s", clock.URL, got)
	}
}

// TestDeviceSource_StaleStoreOverrideReportsDiscovered covers the reporting
// side of the fall-through fix: once rediscoverClock swaps away from an
// unreachable store override to a discovered candidate (in-memory only), the
// store entry still exists but no longer matches the effective URL, so
// deviceSource must report "discovered", not "store".
func TestDeviceSource_StaleStoreOverrideReportsDiscovered(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "http://127.0.0.1:9"); err != nil { // stale store override
		t.Fatal(err)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	a.initDeviceDiscovery(context.Background())

	if got := a.deviceSource(); got != "discovered" {
		t.Fatalf("deviceSource()=%q want discovered (effective url=%s)", got, a.cfg.Load().AWTRIX.HTTPBaseURL)
	}
}

// TestDeviceSource_MatchingStoreOverrideStillReportsStore is the regression
// guard: when the store value still equals the effective URL (the common
// case — no stale-pin incident), deviceSource must keep reporting "store".
func TestDeviceSource_MatchingStoreOverrideStillReportsStore(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := a.applyDeviceBaseURL("http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if got := a.deviceSource(); got != "store" {
		t.Fatalf("deviceSource()=%q want store", got)
	}
}

func TestAutoRediscoverEnabled_DefaultsToTrue(t *testing.T) {
	var cfg AWTRIXConfig
	if !cfg.AutoRediscoverEnabled() {
		t.Fatalf("expected AutoRediscoverEnabled()=true when field is nil")
	}
}

func TestAutoRediscoverEnabled_ExplicitFalse(t *testing.T) {
	f := false
	cfg := AWTRIXConfig{AutoRediscover: &f}
	if cfg.AutoRediscoverEnabled() {
		t.Fatalf("expected AutoRediscoverEnabled()=false when field is explicitly false")
	}
}

// TestStartDeviceWatch_SwapsOnUnreachableCurrent drives the loop with a fast
// interval, an unreachable current URL, and a reachable browse candidate:
// it polls (with a timeout) for the swap to happen, then cancels and asserts
// the goroutine returns promptly (no leak).
func TestStartDeviceWatch_SwapsOnUnreachableCurrent(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" }) // unreachable
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // safety net if an assertion below fails before the explicit cancel
	done := make(chan struct{})
	go func() {
		a.StartDeviceWatch(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.cfg.Load().AWTRIX.HTTPBaseURL == clock.URL {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.cfg.Load().AWTRIX.HTTPBaseURL; got != clock.URL {
		t.Fatalf("expected swap to %s within timeout, got %s", clock.URL, got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("StartDeviceWatch did not return promptly after ctx cancel")
	}
}
