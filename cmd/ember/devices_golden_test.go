package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
	"github.com/tarakanof/ember/internal/pomodoro"
)

const customKnobConfig = `{
	"brightness": {"follow_ember": false, "level": 200, "floor": 20, "startup": 120},
	"pages": [{"id": "pomodoro", "on": true}, {"id": "bot", "on": true}, {"id": "nowplaying", "on": true},
		{"id": "weather", "on": false}, {"id": "future-page", "on": false}],
	"home": "pomodoro",
	"poll_ms": 3000,
	"bot": {"sleepy_after_s": 600, "demo_hold_s": 30, "source_label": false, "working_ring": false},
	"diagnostics": "basic",
	"stats_interval_s": 120,
	"live_interval_s": 2,
	"display": {"fast_link": false}
}`

func assertDeviceGolden(t *testing.T, name string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", name, err, body)
	}
	buf.WriteByte('\n')
	compareGolden(t, filepath.Join("testdata", "devices", name+".json"), buf.Bytes())
}

func mustOK(t *testing.T, what string, resp *http.Response, body []byte) []byte {
	t.Helper()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("%s = %d: %s", what, resp.StatusCode, body)
	}
	return body
}

func putKnobConfig(t *testing.T, srv *httptest.Server, id, body string) {
	t.Helper()
	resp, b := devReq(t, srv, "PUT", "/v1/devices/"+id+"/config", testToken, body)
	mustOK(t, "config put", resp, b)
}

func goldenView(t *testing.T, f *viewFixture) []byte {
	t.Helper()
	body, _, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDeviceViewGolden(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		f := newViewFixture(t)
		now := f.clk.Now()
		f.app.knobStats.now = f.clk.Now
		f.app.updateConfig(func(c *Config) {
			c.Weather.Enabled = true
			c.Weather.Provider = "open-meteo"
			c.Weather.Latitude, c.Weather.Longitude = lonLat, lonLon
		})
		f.app.weather.obs = weatherObservation{Condition: "rain", ConditionCode: "61", TempC: 12.5, FetchedAt: now.Add(-5 * time.Minute)}
		f.app.weather.have = true
		teal := "#00c8c8"
		f.app.Upsert(StatusRequest{Source: "studio", Tool: "claude", Session: "s1", State: "waiting", SourceColor: &teal})
		f.app.Upsert(StatusRequest{Source: "studio", Tool: "claude", Session: "s2", State: "waiting", SourceColor: &teal})
		f.app.Upsert(StatusRequest{Source: "laptop", Tool: "codex", Session: "s3", State: "waiting"})
		f.app.Upsert(StatusRequest{Source: "laptop", Tool: "codex", Session: "s4", State: "running"})
		f.eng.Start(pomodoro.PhaseFocus)
		putKnobConfig(t, f.srv, f.m.ID, `{"diagnostics":"full","pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
		resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":300}`)
		mustOK(t, "live", resp, b)
		rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: "Example Song",
			Artist: "Example Artist", Album: "Example Album", TrackID: "4242", DurationMS: 330_000, PositionMS: 61_000, Volume: new(40)}
		if _, err := f.app.nowPlaying.reg.Report(rep, now.Add(-2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := f.app.nowPlaying.reg.SetArt("plex", "Plexamp", "4242", nowplaying.Album, nowplaying.NewImage([]byte("album"))); err != nil {
			t.Fatal(err)
		}
		if !f.app.nowPlaying.reg.SetArtistArt("plex", "Plexamp", "Example Artist", nowplaying.NewImage([]byte("artist"))) {
			t.Fatal("artist art not set")
		}
		assertDeviceGolden(t, "view_full", goldenView(t, f))
	})
	t.Run("minimal", func(t *testing.T) {
		f := newViewFixture(t)
		f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false; c.Weather.Enabled = false })
		assertDeviceGolden(t, "view_minimal", goldenView(t, f))
	})
	t.Run("nowplaying_none", func(t *testing.T) {
		f := newViewFixture(t)
		putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
		assertDeviceGolden(t, "view_nowplaying_none", goldenView(t, f))
	})
}

func goldenCheckinRequests(t *testing.T) (minimal, full map[string]any, dump []byte) {
	t.Helper()
	dump, dumpID := fakeDump(7, 4096)
	minimal = map[string]any{
		"config_version": 1, "fw": "0.9.13", "heap_internal_free": 47104, "heap_internal_largest": 31744,
		"ip": "192.0.2.10", "rssi": -58, "uptime_s": 812,
	}
	full = map[string]any{
		"config_version": 2,
		"diag": map[string]any{
			"boots":             12,
			"crash":             map[string]any{"elf": "a1b2c3d4", "id": dumpID, "pc": "0x4201a2b3", "reason": "panic", "size": len(dump), "task": "ember"},
			"heap_internal_min": 30120, "heap_largest_min": 22528, "reset_reason": "panic",
			"stack_free": map[string]any{"ember": 1220, "lvgl": 2048},
		},
		"fw": "0.9.13", "fw_build": runningBuild,
		"heap_internal_free": 47104, "heap_internal_largest": 31744, "ip": "192.0.2.10",
		"link_fallback": false, "link_mhz": 80,
		"ota":  map[string]any{"image": "valid", "phase": "idle", "rollback": true, "slot": 0},
		"rssi": -58,
		"stats": map[string]any{
			"cpu_pct": []float64{12.5, 3.5}, "fps": 29.5, "frame_ms_avg": 12.25, "frame_ms_max": 40,
			"heap_internal_min": 30000, "period_ms": 60000, "psram_free": 7000000, "psram_largest": 6000000,
			"psram_min": 6500000, "req_fail": 2, "req_ms_avg": 35.5, "req_ms_max": 120, "req_ok": 28,
			"reset_reason": "poweron", "temp_c": 41.5,
		},
		"uptime_s": 812,
		"wifi":     map[string]any{"bssid": "02:00:5e:00:00:01", "channel": 6, "disconnects": 3, "last_reason": 203, "rssi_min": -83},
	}
	return minimal, full, dump
}

func TestDeviceCheckinGolden(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	clk := &stepClock{now: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)}
	app.devices.now = clk.Now
	app.knobStats.now = clk.Now
	k := otaKnob{app: app, srv: srv, knob: mintKnob(t, srv, http.StatusCreated)}

	minimal, full, dump := goldenCheckinRequests(t)
	encode := func(name string, v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		assertDeviceGolden(t, name, b)
		return string(b)
	}
	minimalBody := encode("checkin_req_minimal", minimal)
	fullBody := encode("checkin_req_full", full)
	post := func(body string) []byte {
		t.Helper()
		resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", k.knob.Token, body)
		return mustOK(t, "checkin", resp, b)
	}

	assertDeviceGolden(t, "checkin_reply_current", post(minimalBody))

	putKnobConfig(t, srv, k.knob.ID, `{"diagnostics":"full"}`)
	assertDeviceGolden(t, "checkin_reply_config", post(minimalBody))

	resp, b := devReq(t, srv, "POST", "/v1/devices/"+k.knob.ID+"/stats/live", testToken, `{"seconds":300}`)
	mustOK(t, "live", resp, b)
	assertDeviceGolden(t, "checkin_reply_coredump", post(fullBody))

	k.upload(t, fakeFirmware(fwOpts{version: "0.9.14"}), "")
	k.target(t, "0.9.14")

	crashID := full["diag"].(map[string]any)["crash"].(map[string]any)["id"].(string)
	resp, b = putDump(t, srv, k.knob.Token, crashID, bytes.NewReader(dump))
	mustOK(t, "coredump", resp, b)
	assertDeviceGolden(t, "checkin_reply_ota", post(fullBody))

	var last *deviceCheckin
	for _, d := range app.devices.list() {
		if d.ID == k.knob.ID {
			last = d.LastCheckin
		}
	}
	switch {
	case last == nil:
		t.Fatal("no last checkin stored")
	case last.FW != "0.9.13" || last.FWBuild != runningBuild || last.IP != "192.0.2.10" || last.LinkMHz != 80:
		t.Errorf("scalar fields not stored: %+v", last)
	case last.Wifi == nil || last.Wifi.Channel != 6:
		t.Errorf("wifi dropped: %+v", last.Wifi)
	case last.Diag == nil || last.Diag.Crash == nil || last.Diag.Crash.ID != crashID || last.Diag.StackFree["lvgl"] != 2048:
		t.Errorf("diag dropped: %+v", last.Diag)
	case last.OTA == nil || last.OTA.Image != "valid":
		t.Errorf("ota dropped: %+v", last.OTA)
	}
	if _, latest := app.knobStats.points(k.knob.ID, "15m", clk.Now()); latest == nil || latest.RenderFPS == nil {
		t.Errorf("stats not recorded: %+v", latest)
	}
}

func TestDeviceConfigGolden(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	get := func() []byte {
		t.Helper()
		resp, b := devReq(t, srv, "GET", "/v1/devices/self/config", m.Token, "")
		return mustOK(t, "self config", resp, b)
	}
	assertDeviceGolden(t, "config_default", get())
	putKnobConfig(t, srv, m.ID, customKnobConfig)
	assertDeviceGolden(t, "config_custom", get())
}
