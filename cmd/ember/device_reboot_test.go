package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestRebootDetected(t *testing.T) {
	cases := []struct {
		name string
		prev deviceProbe
		cur  deviceProbe
		want bool
	}{
		{"first probe of the process is never a reboot",
			deviceProbe{}, deviceProbe{seen: true, reachable: true, uptimeSec: 5}, false},
		{"uptime climbing is steady state",
			deviceProbe{seen: true, reachable: true, uptimeSec: 100},
			deviceProbe{seen: true, reachable: true, uptimeSec: 130}, false},
		{"uptime going backwards is a reboot",
			deviceProbe{seen: true, reachable: true, uptimeSec: 3874},
			deviceProbe{seen: true, reachable: true, uptimeSec: 12}, true},
		{"unreachable to reachable is a reboot",
			deviceProbe{seen: true, reachable: false},
			deviceProbe{seen: true, reachable: true, uptimeSec: 900}, true},
		{"still unreachable is not a reboot",
			deviceProbe{seen: true, reachable: true, uptimeSec: 100},
			deviceProbe{seen: true, reachable: false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rebootDetected(c.prev, c.cur); got != c.want {
				t.Fatalf("rebootDetected(%+v, %+v) = %v, want %v", c.prev, c.cur, got, c.want)
			}
		})
	}
}

func TestProbeDevice_ReadsUptimeFromNGDeviceInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"uid":"e868e705ffb8","boardType":"awtrixng","uptimeSeconds":3874}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = srv.URL })
	got := a.probeDevice(context.Background())
	if !got.seen || !got.reachable || got.uptimeSec != 3874 {
		t.Fatalf("probeDevice = %+v, want seen+reachable with uptime 3874", got)
	}

	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	if got := a.probeDevice(context.Background()); !got.seen || got.reachable {
		t.Fatalf("probeDevice(unreachable) = %+v, want seen and !reachable", got)
	}
}

// TestStartDeviceWatch_RepublishesOnReboot is the event-driven replacement for
// the removed blind 30s Pomodoro re-assert loop: the watcher notices the clock's
// uptime went backwards and queues a republish command on the coordinator.
func TestStartDeviceWatch_RepublishesOnReboot(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		uptime := 3874
		if calls.Add(1) > 2 { // first tick probes twice (rediscover + uptime)
			uptime = 7
		}
		_, _ = w.Write([]byte(`{"uid":"e868e705ffb8","boardType":"awtrixng","uptimeSeconds":` +
			strconv.Itoa(uptime) + `}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = srv.URL })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.StartDeviceWatch(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case cmd := <-a.coord.cmds:
			if cmd.kind == cmdRepublish {
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("StartDeviceWatch did not return after ctx cancel")
				}
				return
			}
		case <-deadline:
			t.Fatal("no cmdRepublish queued after the device's uptime reset")
		}
	}
}
