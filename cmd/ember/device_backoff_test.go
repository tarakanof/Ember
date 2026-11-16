package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

type backoffRig struct {
	a      *App
	now    time.Time
	browse int
	cands  []discovery.Candidate
	path   string
}

func newBackoffRig(t *testing.T) *backoffRig {
	t.Helper()
	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	return rigFor(a)
}

func rigFor(a *App) *backoffRig {
	r := &backoffRig{a: a, now: time.Unix(1_700_000_000, 0)}
	r.a.rediscoverNow = func() time.Time { return r.now }
	r.a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		r.browse++
		return r.cands, nil
	}
	return r
}

func (r *backoffRig) tick() {
	r.a.rediscoverClock(context.Background())
	r.now = r.now.Add(deviceWatchInterval)
}

func TestRediscoverClock_BrowseBacksOffWhenNothingAnswers(t *testing.T) {
	r := newBackoffRig(t)
	var browsedAt []time.Duration
	start := r.now
	for i := 0; i < 100; i++ {
		before := r.browse
		at := r.now.Sub(start)
		r.tick()
		if r.browse > before {
			browsedAt = append(browsedAt, at)
		}
	}
	want := []time.Duration{0, 30 * time.Second, 90 * time.Second, 210 * time.Second, 450 * time.Second, 930 * time.Second, 1530 * time.Second, 2130 * time.Second, 2730 * time.Second}
	for i, w := range want {
		if i >= len(browsedAt) || browsedAt[i] != w {
			t.Fatalf("browse times = %v, want prefix %v", browsedAt, want)
		}
	}
	if got := browsedAt[len(browsedAt)-1] - browsedAt[len(browsedAt)-2]; got != browseBackoffCap {
		t.Fatalf("steady gap = %v, want cap %v", got, browseBackoffCap)
	}
}

func TestRediscoverClock_BackoffResets(t *testing.T) {
	type resetCase struct {
		rig   func(t *testing.T) *backoffRig
		reset func(t *testing.T, r *backoffRig)
	}
	plain := func(t *testing.T) *backoffRig { return newBackoffRig(t) }
	cases := map[string]resetCase{
		"candidate found": {plain, func(t *testing.T, r *backoffRig) {
			r.cands = []discovery.Candidate{{BaseURL: "http://127.0.0.1:9", UID: "u"}}
			for n := r.browse; r.browse == n; {
				r.tick()
			}
			r.cands = nil
		}},
		"url changed": {plain, func(t *testing.T, r *backoffRig) {
			r.a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:8" })
		}},
		"unset state changed": {plain, func(t *testing.T, r *backoffRig) {
			r.a.updateConfig(func(c *Config) { c.AWTRIX.clockUnset = !c.AWTRIX.clockUnset })
		}},
		"clock settings put": {plain, func(t *testing.T, r *backoffRig) {
			if w := putDeviceConfig(r.a, `{"base_url":"http://127.0.0.1:9"}`); w.Code != http.StatusOK {
				t.Fatalf("put: %d %s", w.Code, w.Body.String())
			}
		}},
		"discover finds a candidate": {plain, func(t *testing.T, r *backoffRig) {
			r.cands = []discovery.Candidate{{BaseURL: "http://10.0.0.9", UID: "u"}}
			w := httptest.NewRecorder()
			r.a.handleDeviceDiscover(w, httptest.NewRequest("GET", "/v1/device/discover", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("discover: %d", w.Code)
			}
			r.cands = nil
		}},
		"admin reload": {
			func(t *testing.T) *backoffRig {
				a, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://127.0.0.1:9"}}`)
				r := rigFor(a)
				r.path = path
				return r
			},
			func(t *testing.T, r *backoffRig) {
				if got := reloadStatus(t, r.a, r.path, `{"awtrix":{"http_base_url":"http://127.0.0.1:9"}}`); got != http.StatusOK {
					t.Fatalf("reload = %d", got)
				}
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := c.rig(t)
			for i := 0; i < 6; i++ {
				r.tick()
			}
			before := r.browse
			r.tick()
			if r.browse != before {
				t.Fatalf("expected a backed-off tick to skip the browse")
			}
			c.reset(t, r)
			before = r.browse
			r.tick()
			if r.browse != before+1 {
				t.Fatalf("browse after reset: got %d new, want 1", r.browse-before)
			}
			before = r.browse
			r.tick()
			r.tick()
			if r.browse != before+1 {
				t.Fatalf("backoff should restart at 30 s: %d browses in two ticks", r.browse-before)
			}
		})
	}
}

func TestRediscoverClock_ReappearedClockSeenDuringBackoff(t *testing.T) {
	up := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"uid":"u","boardType":"awtrixng"}`))
	}))
	defer srv.Close()
	r := newBackoffRig(t)
	r.a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = srv.URL })
	for i := 0; i < 8; i++ {
		r.tick()
	}
	before := r.browse
	up = true
	r.tick()
	if r.browse != before {
		t.Fatalf("a reachable clock must not browse")
	}
	if got := r.a.lastRediscoverResult.Load(); got != "reachable" {
		t.Fatalf("result = %v, want reachable", got)
	}
	up = false
	r.tick()
	if r.browse != before+1 {
		t.Fatalf("reachable should reset the backoff; browses = %d", r.browse-before)
	}
}
