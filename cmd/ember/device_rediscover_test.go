package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp(defaultConfig(), &recordingPublisher{}, discardLogger())
	return a
}

func TestRediscoverClock_SwapsWhenCurrentUnreachable(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}
	changed := a.rediscoverClock(context.Background())
	if !changed || a.cfg.Load().effectiveClockURL() != clock.URL {
		t.Fatalf("expected swap to %s, got changed=%v url=%s", clock.URL, changed, a.cfg.Load().effectiveClockURL())
	}
	if got := a.lastRediscoverResult.Load(); got != "swapped" {
		t.Fatalf("lastRediscoverResult=%v want swapped", got)
	}
	if a.lastRediscoverAt.Load() == 0 {
		t.Fatalf("lastRediscoverAt not recorded")
	}
	if a.deviceSource() != clockSourceDiscovered {
		t.Fatalf("expected source discovered after swap")
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

func TestRediscoverClock_SkipsBrowseWhenContextCancelled(t *testing.T) {
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	browsed := false
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { browsed = true; return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.rediscoverClock(ctx) || browsed {
		t.Fatalf("cancelled rediscover must not browse: browsed=%v", browsed)
	}
	if got := a.lastRediscoverResult.Load(); got != "cancelled" {
		t.Fatalf("lastRediscoverResult=%v want cancelled", got)
	}
}

func TestRediscoverClock_CancelDuringBrowseIsNotNoDevice(t *testing.T) {
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { cancel(); return nil, nil }
	if a.rediscoverClock(ctx) {
		t.Fatalf("cancelled browse must not swap")
	}
	if got := a.lastRediscoverResult.Load(); got != "cancelled" {
		t.Fatalf("lastRediscoverResult=%v want cancelled", got)
	}
}

func TestNewServerApp_WiresRealClockBrowse(t *testing.T) {
	a := newServerApp(defaultConfig(), discardLogger())
	if reflect.ValueOf(a.browseFn).Pointer() != reflect.ValueOf(discovery.BrowseAWTRIX).Pointer() {
		t.Fatal("server app must browse with discovery.BrowseAWTRIX")
	}
}

func TestNewApp_DefaultBrowseNeverReachesLAN(t *testing.T) {
	a := NewApp(defaultConfig(), &recordingPublisher{}, discardLogger())
	got, err := a.browseFn(context.Background(), time.Millisecond)
	if !errors.Is(err, errBrowseNotWired) || len(got) != 0 {
		t.Fatalf("default browseFn = (%+v, %v), want (none, errBrowseNotWired)", got, err)
	}
}

func TestRediscoverClock_NoDeviceFound(t *testing.T) {
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
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

func TestInitDeviceDiscovery_FallsThroughUnreachableStoreOverride(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "http://127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	a.settings.reapply()
	a.initDeviceDiscovery(context.Background())

	if got := a.cfg.Load().effectiveClockURL(); got != clock.URL {
		t.Fatalf("expected fall-through to discovered clock %s, got %s", clock.URL, got)
	}
}

func TestDeviceSource_StaleStoreOverrideReportsDiscovered(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestAppWithStore(t)
	if err := a.store.PutSetting(deviceBaseURLKey, "http://127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	a.settings.reapply()
	a.initDeviceDiscovery(context.Background())

	if got := a.deviceSource(); got != "discovered" {
		t.Fatalf("deviceSource()=%q want discovered (effective url=%s)", got, a.cfg.Load().effectiveClockURL())
	}
}

func TestDeviceSource_MatchingStoreOverrideStillReportsStore(t *testing.T) {
	a := newTestAppWithStore(t)
	if err := putClockOverride(a, "http://10.0.0.5"); err != nil {
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

func TestStartDeviceWatch_SwapsOnUnreachableCurrent(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()

	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.StartDeviceWatch(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.cfg.Load().effectiveClockURL() == clock.URL {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.cfg.Load().effectiveClockURL(); got != clock.URL {
		t.Fatalf("expected swap to %s within timeout, got %s", clock.URL, got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("StartDeviceWatch did not return promptly after ctx cancel")
	}
}
