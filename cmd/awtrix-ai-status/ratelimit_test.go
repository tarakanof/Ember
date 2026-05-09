package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeClock is a synchronous, manually-advanced clock for limiter tests.
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

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
		"[::1]:8080":        "::1",
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

func TestRateLimit_AppliesPostAuth(t *testing.T) {
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

	// Wrong token: expect 401 (auth runs first; no token consumed).
	req1, err := http.NewRequest("POST", srv.URL+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req1.Header.Set("Authorization", "Bearer wrong")
	resp1, err := srv.Client().Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: code = %d, want 401", resp1.StatusCode)
	}

	// First authed call passes auth + rate limit; reaches handler (4xx but
	// not 401 or 429 is the point — the handler may 400 because body is nil).
	req2, err := http.NewRequest("POST", srv.URL+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Authorization", "Bearer tok")
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusUnauthorized || resp2.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("first authed call: code = %d, want neither 401 nor 429", resp2.StatusCode)
	}

	// Second authed call: bucket drained, expect 429.
	req3, err := http.NewRequest("POST", srv.URL+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req3.Header.Set("Authorization", "Bearer tok")
	resp3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second authed call: code = %d, want 429", resp3.StatusCode)
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
