package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

func clockAccessFor(url string, timeoutSec int) *clockAccess {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = url
	cfg.AWTRIX.TimeoutSeconds = timeoutSec
	return newClockAccess(func() *Config { return &cfg })
}

// One URL rule for every call: empty is "not configured", anything that isn't
// an absolute http(s) URL never gets dialled, a trailing slash is dropped.
func TestClockAccessURLRule(t *testing.T) {
	if _, err := clockAccessFor("", 10).client(callMenu); !errors.Is(err, errClockNotConfigured) {
		t.Fatalf("empty url err = %v, want errClockNotConfigured", err)
	}
	for _, bad := range []string{"file:///etc/passwd", "192.168.0.66", "gopher://x"} {
		if _, err := clockAccessFor(bad, 10).client(callPublish); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	cl, err := clockAccessFor("http://192.168.0.66/", 10).client(callProbe)
	if err != nil || cl.BaseURL() != "http://192.168.0.66" {
		t.Fatalf("client = %v, %v", cl, err)
	}
}

// The timeout table. The lossy-link tuning lives in these numbers: the probe
// must fit its watch tick, capabilities must not delay boot, and the publish
// ceiling is the config's (the coordinator narrows each attempt itself).
func TestClockAccessCallClassTimeouts(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.TimeoutSeconds = 10
	zero := defaultConfig()
	zero.AWTRIX.TimeoutSeconds = 0
	for _, c := range []struct {
		class callClass
		cfg   *Config
		want  time.Duration
	}{
		{callPublish, &cfg, 10 * time.Second},
		{callMenu, &cfg, 8 * time.Second},
		{callProbe, &cfg, 1500 * time.Millisecond},
		{callCapabilities, &cfg, 2 * time.Second},
		{callDoctor, &cfg, 10 * time.Second},
		{callDoctor, &zero, 2 * time.Second},
	} {
		if got := c.class.timeout(c.cfg); got != c.want {
			t.Errorf("class %d timeout = %v, want %v", c.class, got, c.want)
		}
	}
}

// Every call reads the live URL, so a rediscovery swap applies to the next one.
func TestClockAccessFollowsTheLiveURL(t *testing.T) {
	hits := map[string]int{}
	srv := func(name string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits[name]++ }))
		t.Cleanup(s.Close)
		return s
	}
	a, b := srv("a"), srv("b")
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = a.URL
	k := newClockAccess(func() *Config { return &cfg })
	if _, err := k.fetch(context.Background(), (*awtrix.Client).RawDevice); err != nil {
		t.Fatal(err)
	}
	cfg.AWTRIX.HTTPBaseURL = b.URL
	if _, err := k.fetch(context.Background(), (*awtrix.Client).RawDevice); err != nil {
		t.Fatal(err)
	}
	if hits["a"] != 1 || hits["b"] != 1 {
		t.Fatalf("hits = %v, want one each", hits)
	}
}

// fetch turns a refusal into the clock's own *awtrix.APIError, and
// writeClockError relays it exactly as the raw-reply path always has.
func TestClockAccessErrorMapParity(t *testing.T) {
	body := `{"error":{"code":"validationFailed","message":"bad","field":"brightness"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := clockAccessFor(srv.URL, 10).fetch(context.Background(), (*awtrix.Client).RawSettings)
	var apiErr *awtrix.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *awtrix.APIError", err)
	}
	viaErr, viaReply := httptest.NewRecorder(), httptest.NewRecorder()
	writeClockError(viaErr, err)
	writeDeviceError(viaReply, http.StatusUnprocessableEntity, []byte(body))
	if viaErr.Code != viaReply.Code || viaErr.Body.String() != viaReply.Body.String() {
		t.Fatalf("writeClockError = %d %s, writeDeviceError = %d %s",
			viaErr.Code, viaErr.Body, viaReply.Code, viaReply.Body)
	}

	w := httptest.NewRecorder()
	writeClockError(w, errClockNotConfigured)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("unreachable/unconfigured → %d, want 502", w.Code)
	}
}

// A system write waiting behind another one gives up when its request does,
// without touching the clock.
func TestClockAccessSystemLockHonoursContext(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	k := clockAccessFor(srv.URL, 10)
	k.systemLock <- struct{}{} // another writer holds it
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := k.updateSystem(ctx, func(map[string]any) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if hits != 0 {
		t.Fatalf("clock saw %d requests while the lock was held", hits)
	}
}
