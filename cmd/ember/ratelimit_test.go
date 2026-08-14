package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeClock is a thread-safe, manually-advanced clock for limiter and coordinator tests.
type fakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestLimiter(t *testing.T, burst int, refill float64) (*IPLimiter, *App, *fakeClock) {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.Burst = burst
	cfg.RateLimit.RefillPerSec = refill
	cfg.RateLimit.IdleEvictSeconds = 60
	cfg.applyDefaults()

	app := &App{}
	app.cfg.Store(&cfg)
	app.metrics = newMetrics()

	clock := newFakeClock()
	lim := NewIPLimiter(app)
	lim.clock = clock.Now
	return lim, app, clock
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIPLimiter_AllowsBurstThenDenies(t *testing.T) {
	lim, _, _ := newTestLimiter(t, 3, 0)
	for i := 0; i < 3; i++ {
		ok, _ := lim.Allow("192.0.2.1")
		if !ok {
			t.Fatalf("call %d: expected allow, got deny", i+1)
		}
	}
	ok, retryAfter := lim.Allow("192.0.2.1")
	if ok {
		t.Fatal("4th call: expected deny, got allow")
	}
	if retryAfter < 1 {
		t.Errorf("retryAfter = %d, want >= 1", retryAfter)
	}
}

func TestIPLimiter_RefillsToBurstCap(t *testing.T) {
	lim, _, clock := newTestLimiter(t, 5, 1.0)
	for i := 0; i < 5; i++ {
		lim.Allow("192.0.2.2")
	}
	clock.Advance(10 * time.Second)
	for i := 0; i < 5; i++ {
		if ok, _ := lim.Allow("192.0.2.2"); !ok {
			t.Fatalf("post-refill call %d denied", i+1)
		}
	}
	if ok, _ := lim.Allow("192.0.2.2"); ok {
		t.Error("6th post-refill call should deny (burst cap)")
	}
}

func TestIPLimiter_FractionalRefill(t *testing.T) {
	lim, _, clock := newTestLimiter(t, 2, 0.5)
	lim.Allow("192.0.2.3")
	lim.Allow("192.0.2.3")

	clock.Advance(2 * time.Second)
	if ok, _ := lim.Allow("192.0.2.3"); !ok {
		t.Error("after 2s @ 0.5/sec, expected 1 token available")
	}
	if ok, _ := lim.Allow("192.0.2.3"); ok {
		t.Error("after consuming the refill, expected deny")
	}
}

func TestIPLimiter_PerIPIsolation(t *testing.T) {
	lim, _, _ := newTestLimiter(t, 1, 0)
	if ok, _ := lim.Allow("192.0.2.10"); !ok {
		t.Fatal("first request from .10 should be allowed")
	}
	if ok, _ := lim.Allow("192.0.2.20"); !ok {
		t.Fatal(".20 should have its own bucket and be allowed")
	}
	if ok, _ := lim.Allow("192.0.2.10"); ok {
		t.Error(".10's bucket should be drained")
	}
}

func TestIPLimiter_DisabledShortCircuits(t *testing.T) {
	lim, app, _ := newTestLimiter(t, 1, 0)
	cfg := *app.cfg.Load()
	cfg.RateLimit.Disabled = true
	app.cfg.Store(&cfg)

	for i := 0; i < 100; i++ {
		if ok, _ := lim.Allow("192.0.2.99"); !ok {
			t.Fatalf("call %d denied while Disabled=true", i+1)
		}
	}
}

func TestIPLimiter_ZeroBurstShortCircuits(t *testing.T) {
	lim, app, _ := newTestLimiter(t, 5, 1)
	cfg := *app.cfg.Load()
	cfg.RateLimit.Burst = 0
	app.cfg.Store(&cfg)

	if ok, _ := lim.Allow("192.0.2.50"); !ok {
		t.Error("Burst=0 should short-circuit to allow (treated as disabled)")
	}
}

func TestIPLimiter_RetryAfterHonoursPartialTokens(t *testing.T) {
	lim, _, _ := newTestLimiter(t, 1, 1.0)
	lim.Allow("192.0.2.40")
	_, ra := lim.Allow("192.0.2.40")
	if ra != 1 {
		t.Errorf("retryAfter = %d, want 1 (1 token at 1/sec = 1s)", ra)
	}

	lim2, _, _ := newTestLimiter(t, 1, 0.5)
	lim2.Allow("192.0.2.41")
	_, ra2 := lim2.Allow("192.0.2.41")
	if ra2 != 2 {
		t.Errorf("retryAfter = %d, want 2 (1 token at 0.5/sec = 2s)", ra2)
	}
}

func TestIPLimiter_LastSeenUpdatesOnDeniedRequest(t *testing.T) {
	lim, _, clock := newTestLimiter(t, 1, 0)
	lim.Allow("192.0.2.30")

	clock.Advance(30 * time.Second)
	lim.Allow("192.0.2.30")

	clock.Advance(40 * time.Second)
	lim.sweep()

	lim.mu.Lock()
	_, present := lim.buckets["192.0.2.30"]
	lim.mu.Unlock()
	if !present {
		t.Error("bucket evicted while still active (denied requests should bump lastSeen)")
	}
}

func TestIPLimiter_SweeperEvictsIdle(t *testing.T) {
	lim, _, clock := newTestLimiter(t, 1, 0)
	lim.Allow("192.0.2.31")

	clock.Advance(120 * time.Second)
	lim.sweep()

	lim.mu.Lock()
	_, present := lim.buckets["192.0.2.31"]
	lim.mu.Unlock()
	if present {
		t.Error("idle bucket should have been evicted")
	}
}

func TestIPLimiter_BurstReductionClampsImmediately(t *testing.T) {
	lim, app, _ := newTestLimiter(t, 10, 0)
	cfg := *app.cfg.Load()
	cfg.RateLimit.Burst = 3
	app.cfg.Store(&cfg)

	for i := 0; i < 3; i++ {
		if ok, _ := lim.Allow("192.0.2.60"); !ok {
			t.Fatalf("call %d denied after burst reduction", i+1)
		}
	}
	if ok, _ := lim.Allow("192.0.2.60"); ok {
		t.Error("burst should clamp immediately to 3 on the next Allow")
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	cases := map[string]string{
		"192.168.1.5:54321": "192.168.1.5",
		"[::1]:3627":        "::1",
		"10.0.0.1":          "10.0.0.1",
	}
	for in, want := range cases {
		req := httptest.NewRequest("POST", "/v1/status", nil)
		req.RemoteAddr = in
		got := clientIP(req)
		if got != want {
			t.Errorf("clientIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimit_429Response(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.Burst = 1
	cfg.RateLimit.RefillPerSec = 0.5
	cfg.applyDefaults()

	app := &App{logger: discardLogger()}
	app.cfg.Store(&cfg)
	app.limiter = NewIPLimiter(app)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := rateLimit(app, next)

	req1 := httptest.NewRequest("POST", "/v1/status", nil)
	req1.RemoteAddr = "192.0.2.71:1234"
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: code = %d, want 200", rec1.Code)
	}

	req2 := httptest.NewRequest("POST", "/v1/status", nil)
	req2.RemoteAddr = "192.0.2.71:1234"
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call: code = %d, want 429", rec2.Code)
	}
	if got := rec2.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want \"2\"", got)
	}
	if got := rec2.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	body := rec2.Body.String()
	if body != `{"error":"rate limit exceeded"}`+"\n" {
		t.Errorf("body = %q", body)
	}
}

func TestRateLimit_CountsUnauthorizedAttempts(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = "tok"
	cfg.RateLimit.Burst = 1
	cfg.RateLimit.RefillPerSec = 0.5
	cfg.applyDefaults()

	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	wrongToken := func() int {
		req, err := http.NewRequest("POST", srv.URL+"/v1/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wrong")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// First unauthorized attempt passes the limiter (consuming the only
	// token in the bucket) and is then rejected by auth → 401.
	if code := wrongToken(); code != http.StatusUnauthorized {
		t.Fatalf("first wrong-token call: code = %d, want 401", code)
	}
	// Second unauthorized attempt from the same IP: the limiter sits outside
	// auth now, so the drained bucket throttles the request before auth runs
	// → 429. This is the point of the reorder: 401s consume rate-limit budget.
	if code := wrongToken(); code != http.StatusTooManyRequests {
		t.Fatalf("second wrong-token call: code = %d, want 429 (401s must consume limiter budget)", code)
	}
}

// Admin endpoints authenticate with the same token as /v1/, so their 401s must
// also consume rate-limit budget — otherwise an attacker throttled on /v1/
// could keep probing the token at full speed via /admin/ 401s.
func TestRateLimit_CountsUnauthorizedAdminAttempts(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = "tok"
	cfg.RateLimit.Burst = 1
	cfg.RateLimit.RefillPerSec = 0.5
	cfg.applyDefaults()

	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	wrongToken := func() int {
		req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wrong")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := wrongToken(); code != http.StatusUnauthorized {
		t.Fatalf("first wrong-token admin call: code = %d, want 401", code)
	}
	if code := wrongToken(); code != http.StatusTooManyRequests {
		t.Fatalf("second wrong-token admin call: code = %d, want 429 (admin 401s must consume limiter budget)", code)
	}
}

func TestRunSweeper_ExitsOnContextCancel(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.IdleEvictSeconds = 100
	cfg.applyDefaults()
	app := &App{}
	app.cfg.Store(&cfg)
	lim := NewIPLimiter(app)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		lim.runSweeper(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSweeper did not exit within 2s of cancellation")
	}
}

func TestRateLimit_DoesNotApplyToReads(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.Burst = 1
	cfg.RateLimit.RefillPerSec = 0.5
	cfg.applyDefaults()

	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	for _, path := range []string{"/healthz", "/state", "/version"} {
		for i := 0; i < 5; i++ {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("%s call %d: %v", path, i+1, err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Errorf("%s call %d: 429 (read endpoints should not be rate-limited)", path, i+1)
			}
		}
	}
}

func TestRateLimit_IncrementsDeniedCounter(t *testing.T) {
	lim, app, _ := newTestLimiter(t, 1, 0)
	app.logger = discardLogger()
	app.limiter = lim

	handler := rateLimit(app, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 1st request: allowed (consumes the only token).
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("POST", "/v1/status", nil)
	r1.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call: status %d, want 200", w1.Code)
	}

	// 2nd + 3rd: denied (counter should advance to 2).
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/status", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests {
			t.Errorf("denied call %d: status %d, want 429", i+2, w.Code)
		}
	}

	if got := app.metrics.rateLimitDenied.Load(); got != 2 {
		t.Errorf("rateLimitDenied = %d, want 2", got)
	}
}

// One Mac runs the menu app (four polled endpoints) plus a producer per
// session, and they all share one source IP. After a network stall they
// reconnect together — 15 requests landing in the same millisecond was
// observed in the wild — so the default burst has to absorb a reconnect
// without answering 429 to a legitimate client.
func TestDefaultRateLimitAbsorbsClientReconnectBurst(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	app := &App{}
	app.cfg.Store(&cfg)
	app.metrics = newMetrics()
	lim := NewIPLimiter(app)
	clock := newFakeClock()
	lim.clock = clock.Now // no refill between calls: this is a single instant

	const reconnectBurst = 20
	for i := 0; i < reconnectBurst; i++ {
		if ok, _ := lim.Allow("192.0.2.9"); !ok {
			t.Fatalf("request %d of a %d-request reconnect burst denied with default rate_limit config (burst=%d)",
				i+1, reconnectBurst, cfg.RateLimit.Burst)
		}
	}
}
