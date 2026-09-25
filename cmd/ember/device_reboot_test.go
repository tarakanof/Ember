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
	t0 := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	up := func(uptime int64, sec int) deviceProbe {
		return deviceProbe{reachable: true, uptimeSec: uptime, at: t0.Add(time.Duration(sec) * time.Second)}
	}
	cases := []struct {
		name string
		prev deviceProbe
		cur  deviceProbe
		want bool
	}{
		{"first answer of the process is never a reboot",
			deviceProbe{}, up(5, 0), false},
		{"uptime climbing with wall time is steady state",
			up(100, 0), up(130, 30), false},
		{"uptime going backwards is a reboot",
			up(3874, 0), up(12, 30), true},
		{"answering after lost probes is not a reboot when uptime kept pace",
			up(900, 0), up(991, 90), false},
		{"probe latency jitter within the slack is not a reboot",
			up(900, 0), up(925, 31), false},
		{"reboot during a gap longer than the old uptime",
			up(20, 0), up(60, 300), true},
		{"unreachable now is not a reboot",
			up(100, 0), deviceProbe{}, false},
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
	if !got.reachable || got.uptimeSec != 3874 || got.at.IsZero() {
		t.Fatalf("probeDevice = %+v, want reachable with uptime 3874 and a timestamp", got)
	}

	a.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "http://127.0.0.1:9" })
	if got := a.probeDevice(context.Background()); got.reachable {
		t.Fatalf("probeDevice(unreachable) = %+v, want !reachable", got)
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
