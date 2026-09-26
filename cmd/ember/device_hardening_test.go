package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

// drainRepublishes counts the cmdRepublish commands queued on the coordinator
// within d. The test App's coordinator isn't running, so the channel is ours.
func drainRepublishes(a *App, d time.Duration) int {
	n := 0
	deadline := time.After(d)
	for {
		select {
		case cmd := <-a.coord.cmds:
			if cmd.kind == cmdRepublish {
				n++
			}
		case <-deadline:
			return n
		}
	}
}

// A burst of boot pings (a Berry script in a boot loop, or any LAN host) must
// not turn into a burst of full republishes: the first goes out at once, the
// rest collapse into a single deferred one, so the last request is still
// honoured.
func TestRepublishAll_CoalescesBurst(t *testing.T) {
	a := newTestApp(t)
	a.republish.minGap = 100 * time.Millisecond

	for i := 0; i < 20; i++ {
		a.RepublishAll("device_boot")
	}
	if got := drainRepublishes(a, 30*time.Millisecond); got != 1 {
		t.Fatalf("immediate republishes = %d, want 1", got)
	}
	if got := drainRepublishes(a, 250*time.Millisecond); got != 1 {
		t.Fatalf("deferred republishes = %d, want 1", got)
	}
	// Once the gap has passed with nothing pending, the next call is immediate.
	time.Sleep(120 * time.Millisecond)
	a.RepublishAll("device_boot")
	if got := drainRepublishes(a, 30*time.Millisecond); got != 1 {
		t.Fatalf("republish after quiet gap = %d, want 1", got)
	}
}

// The hooks are unauthenticated but no longer unthrottled: they share the
// per-IP limiter with /v1/ and /admin/.
func TestDeviceHooks_AreRateLimited(t *testing.T) {
	for _, path := range []string{"/hooks/awtrix/boot", "/hooks/awtrix/button"} {
		t.Run(path, func(t *testing.T) {
			a := newTestApp(t)
			a.updateConfig(func(c *Config) {
				c.RateLimit.Burst = 1
				c.RateLimit.RefillPerSec = 0.001
			})
			h := a.routes()
			codes := make([]int, 2)
			for i := range codes {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("button=left&state=1"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.RemoteAddr = "192.0.2.66:5000"
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				codes[i] = w.Code
			}
			if codes[0] == http.StatusTooManyRequests || codes[1] != http.StatusTooManyRequests {
				t.Fatalf("status codes = %v, want [allowed, 429]", codes)
			}
		})
	}
}

// The form branch used ParseForm, which reads up to 10 MB; a button edge is a
// few dozen bytes.
func TestButtonHook_RejectsOversizedBody(t *testing.T) {
	a := newPomodoroApp(t)
	a.updateConfig(func(c *Config) { c.Pomodoro.ButtonCallback = true })
	form := url.Values{"button": {"middle"}, "state": {"1"}, "pad": {strings.Repeat("x", 4096)}}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/hooks/awtrix/button",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

// deviceAutoPicked is written by the watch goroutine (rediscoverClock) and read
// by HTTP handlers (deviceSource). Run both at once under -race.
func TestDeviceAutoPicked_ConcurrentAccess(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
	}))
	defer clock.Close()
	a := newTestApp(t)
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: clock.URL, UID: "awtrix_test"}}, nil
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
			a.rediscoverClock(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = a.deviceSource()
		}
	}()
	wg.Wait()
	if !a.deviceAutoPicked.Load() {
		t.Fatal("deviceAutoPicked not set after swap")
	}
}

// Browsing mDNS after one dropped 1.5s probe is how the lossy link turned into
// a browse storm; one retry must be spent first.
func TestRediscoverClock_RetriesProbeBeforeBrowsing(t *testing.T) {
	var hits atomic.Int64
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// First probe "lost": drop the connection without answering.
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"uid":"x","boardType":"awtrixng"}`))
	}))
	defer clock.Close()
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	browsed := false
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { browsed = true; return nil, nil }

	if a.rediscoverClock(context.Background()) || browsed {
		t.Fatalf("one lost probe must be retried, not browsed (browsed=%v)", browsed)
	}
	if got := a.lastRediscoverResult.Load(); got != "reachable" {
		t.Fatalf("lastRediscoverResult=%v want reachable", got)
	}
}

// When the browse finds the clock at the URL we already use (the probe was just
// lost twice), nothing changed: no swap, no republish, source untouched.
func TestRediscoverClock_SameURLIsNotASwap(t *testing.T) {
	cases := []struct{ name, cur, found string }{
		{"identical", "http://127.0.0.1:9", "http://127.0.0.1:9"},
		// config.json omits the port; discovery always spells it out.
		{"port-less config URL", "http://127.0.0.1", "http://127.0.0.1:80"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t)
			a.updateConfig(func(cfg *Config) { cfg.AWTRIX.HTTPBaseURL = c.cur })
			a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
				return []discovery.Candidate{{BaseURL: c.found, UID: "x"}}, nil
			}
			if a.rediscoverClock(context.Background()) {
				t.Fatal("rediscovering the current URL reported a swap")
			}
			if a.deviceAutoPicked.Load() {
				t.Fatal("deviceAutoPicked set without a swap")
			}
			if got := a.cfg.Load().AWTRIX.HTTPBaseURL; got != c.cur {
				t.Fatalf("base URL = %q, want unchanged %q", got, c.cur)
			}
		})
	}
}

// A lost probe followed by a good one used to count as a reboot. On a link
// that drops ~44% of requests that meant a republish (and a screen switch)
// every few ticks. Only an uptime that fell behind wall time is a reboot.
func TestStartDeviceWatch_LostProbeIsNotAReboot(t *testing.T) {
	var down atomic.Bool
	var ok, failed atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Quiet without rebooting: rediscover probes and probeDevice all miss.
		if down.Load() {
			failed.Add(1)
			http.Error(w, "lost", http.StatusServiceUnavailable)
			return
		}
		ok.Add(1)
		_, _ = w.Write([]byte(`{"uid":"u","boardType":"awtrixng","uptimeSeconds":5000}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = srv.URL })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { return nil, nil }

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.StartDeviceWatch(ctx, 10*time.Millisecond); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor("a baseline probe", func() bool { return ok.Load() >= 4 })
	down.Store(true)
	// A tick sends 2 rediscover probes then probeDevice: 6 misses cover a probeDevice.
	waitFor("probes to fail", func() bool { return failed.Load() >= 6 })
	answered := ok.Load()
	down.Store(false)
	waitFor("the clock to answer again", func() bool { return ok.Load() >= answered+4 })

	if got := drainRepublishes(a, 50*time.Millisecond); got != 0 {
		t.Fatalf("republishes = %d, want 0 (no reboot happened)", got)
	}
}

func TestSameDeviceURL(t *testing.T) {
	cases := []struct {
		x, y string
		want bool
	}{
		{"http://192.168.0.14", "http://192.168.0.14:80", true},
		{"http://192.168.0.14/", "http://192.168.0.14:80", true},
		{"HTTP://Clock.local", "http://clock.local:80", true},
		{"https://10.0.0.5", "https://10.0.0.5:443", true},
		{"http://192.168.0.14", "http://192.168.0.15:80", false},
		{"http://192.168.0.14:8080", "http://192.168.0.14", false},
		{"http://10.0.0.5", "https://10.0.0.5", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := sameDeviceURL(c.x, c.y); got != c.want {
			t.Errorf("sameDeviceURL(%q, %q) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}
