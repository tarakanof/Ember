package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingKV struct {
	settingsKV
	puts atomic.Int64
}

func (c *countingKV) PutSetting(key, value string) error {
	if key == devicesKey {
		c.puts.Add(1)
	}
	return c.settingsKV.PutSetting(key, value)
}

type failingKV struct {
	settingsKV
	fail atomic.Bool
}

func (f *failingKV) PutSetting(key, value string) error {
	if f.fail.Load() {
		return errors.New("disk full")
	}
	return f.settingsKV.PutSetting(key, value)
}

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func countDeviceWrites(t *testing.T, app *App) (*countingKV, *stepClock) {
	t.Helper()
	kv := &countingKV{settingsKV: app.store}
	clk := &stepClock{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	app.devices.kv = func() settingsKV { return kv }
	app.devices.now = clk.Now
	return kv, clk
}

func TestDeviceCheckinWritesStoreAtMostOncePerPersistInterval(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, clk := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	base := kv.puts.Load()
	for range 5 {
		if resp, _ := checkin(t, srv, m.Token, 1); resp.StatusCode != http.StatusOK {
			t.Fatalf("checkin = %d", resp.StatusCode)
		}
		clk.advance(time.Minute)
	}
	if got := kv.puts.Load() - base; got != 0 {
		t.Fatalf("store writes for 5 checkins inside the interval = %d, want 0", got)
	}
	if seen := app.devices.list()[0].LastCheckin; seen == nil || !seen.SeenAt.Equal(clk.Now().Add(-time.Minute)) {
		t.Fatalf("in-memory last checkin = %+v", seen)
	}
	clk.advance(deviceCheckinPersistInterval)
	checkin(t, srv, m.Token, 1)
	checkin(t, srv, m.Token, 1)
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes after the interval = %d, want 1", got)
	}
}

func TestDeviceAuthenticateDoesNotWriteStore(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, _ := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	base := kv.puts.Load()
	for range 3 {
		if resp, _ := devReq(t, srv, "GET", "/v1/devices/self/config", m.Token, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("self config = %d", resp.StatusCode)
		}
	}
	if got := kv.puts.Load() - base; got != 0 {
		t.Fatalf("store writes for authed reads = %d, want 0", got)
	}
}

func TestDeviceRotationTokenIssueWritesStoreAtOnce(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, _ := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	base := kv.puts.Load()
	_, out := checkin(t, srv, m.Token, 1)
	if out["new_token"] == nil {
		t.Fatalf("checkin = %v, want new_token", out)
	}
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes for the pending-token issue = %d, want 1", got)
	}
	checkin(t, srv, m.Token, 1)
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes for a redelivery = %d, want still 1", got)
	}
}

func TestDeviceLastCheckinSurvivesShutdown(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	checkin(t, srv1, m.Token, 1)
	srv1.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app1.shutdown(ctx, &http.Server{}, &sync.WaitGroup{})

	app2, _ := newDevicesApp(t, db)
	if seen := app2.devices.list()[0].LastCheckin; seen == nil || seen.FW != "0.5.0" {
		t.Fatalf("last checkin after restart = %+v, want the pre-shutdown one", seen)
	}
}

func TestDeviceCheckinSucceedsWhenThePeriodicWriteFails(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	_, clk := countDeviceWrites(t, app)
	kv := &failingKV{settingsKV: app.store}
	app.devices.kv = func() settingsKV { return kv }
	m := mintKnob(t, srv, http.StatusCreated)
	kv.fail.Store(true)
	clk.advance(deviceCheckinPersistInterval)
	resp, out := checkin(t, srv, m.Token, 0)
	if resp.StatusCode != http.StatusOK || out["config_version"] != float64(1) || out["config"] == nil {
		t.Fatalf("checkin with a failing store = %d %v, want 200 with the config", resp.StatusCode, out)
	}
	if err := app.devices.flush(); err == nil {
		t.Fatal("flush after a failed write found nothing dirty")
	}
	kv.fail.Store(false)
	if err := app.devices.flush(); err != nil {
		t.Fatalf("flush once the store recovers = %v", err)
	}
	if blob, _, _ := app.store.GetSetting(devicesKey); !strings.Contains(blob, `"last_checkin"`) {
		t.Fatalf("stored registry lacks the checkin: %s", blob)
	}
}

func TestDeviceLastCheckinWifiSurvivesRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	app1, srv1 := newDevicesApp(t, db)
	m := mintKnob(t, srv1, http.StatusCreated)
	devReq(t, srv1, "POST", "/v1/devices/self/checkin", m.Token,
		`{"fw":"0.9.8","wifi":{"bssid":"78:45:58:4b:c2:cd","channel":6,"disconnects":3,"last_reason":203,"rssi_min":-83}}`)
	srv1.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app1.shutdown(ctx, &http.Server{}, &sync.WaitGroup{})

	app2, _ := newDevicesApp(t, db)
	want := deviceWifi{BSSID: "78:45:58:4b:c2:cd", Channel: 6, Disconnects: 3, LastReason: 203, RSSIMin: -83}
	if got := app2.devices.list()[0].LastCheckin.Wifi; got == nil || *got != want {
		t.Fatalf("wifi after restart = %+v, want %+v", got, want)
	}
}
