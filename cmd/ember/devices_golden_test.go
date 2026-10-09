package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
	"github.com/tarakanof/ember/internal/pomodoro"
)

const exampleDeviceToken = "ekd_EXAMPLE-token-not-real-00000000000000000000"

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
	"display": {"fast_link": false},
	"quiet": {"calm": false, "dim_level": 5}
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

func goldenViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := newViewFixture(t)
	f.app.sessions = f.app.newSessionRegistry(f.clk.Now)
	f.app.knobStats.now = f.clk.Now
	return f
}

func goldenView(t *testing.T, f *viewFixture) []byte {
	t.Helper()
	body, _, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func fullViewScenario(t *testing.T, f *viewFixture) {
	t.Helper()
	now := f.clk.Now()
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
}

func TestDeviceViewGolden(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		f := goldenViewFixture(t)
		fullViewScenario(t, f)
		assertDeviceGolden(t, "view_full", goldenView(t, f))
	})
	t.Run("minimal", func(t *testing.T) {
		f := goldenViewFixture(t)
		f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false; c.Weather.Enabled = false })
		assertDeviceGolden(t, "view_minimal", goldenView(t, f))
	})
	t.Run("nowplaying_none", func(t *testing.T) {
		f := goldenViewFixture(t)
		putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
		assertDeviceGolden(t, "view_nowplaying_none", goldenView(t, f))
	})
	t.Run("quiet", func(t *testing.T) {
		f := goldenViewFixture(t)
		f.app.updateConfig(func(c *Config) {
			c.Pomodoro.Enabled = false
			c.Weather.Enabled = false
			c.QuietHours = QuietHoursConfig{Enabled: true, Start: "11:00", End: "13:00"}
		})
		assertDeviceGolden(t, "view_quiet", goldenView(t, f))
	})
	t.Run("single_host_paused", func(t *testing.T) {
		f := goldenViewFixture(t)
		f.app.updateConfig(func(c *Config) {
			c.Weather.Enabled = true
			c.Weather.Provider = "open-meteo"
			c.Weather.Latitude, c.Weather.Longitude = 0, 0
		})
		f.app.weather.obs = weatherObservation{Condition: "clear", ConditionCode: "0", TempC: -3, FetchedAt: f.clk.Now().Add(-2 * time.Hour)}
		f.app.weather.have = true
		f.app.Upsert(StatusRequest{Source: "studio", Tool: "claude", Session: "s1", State: "running"})
		f.app.Upsert(StatusRequest{Source: "studio", Tool: "codex", Session: "s2", State: "done"})
		f.eng.Start(pomodoro.PhaseFocus)
		f.clk.advance(100 * time.Second)
		f.eng.Pause(f.clk.Now())
		assertDeviceGolden(t, "view_single_host_paused", goldenView(t, f))
	})
}

func TestDeviceViewHeadersContract(t *testing.T) {
	f := newViewFixture(t)
	resp, body := f.get(t, "")
	etag := resp.Header.Get("ETag")
	h := resp.Header
	if resp.StatusCode != http.StatusOK || !regexp.MustCompile(`^"[0-9a-f]{16}"$`).MatchString(etag) {
		t.Fatalf("view = %d, ETag %q, want 200 and a strong 16-hex tag", resp.StatusCode, etag)
	}
	if h.Get("Content-Type") != "application/json" || h.Get("Cache-Control") != "no-cache" || h.Get("X-Ember-View-Wait") != "25" {
		t.Errorf("view headers = %v", h)
	}
	if now, err := strconv.ParseInt(h.Get("X-Ember-Now"), 10, 64); err != nil || time.Since(time.Unix(now, 0)).Abs() > time.Minute {
		t.Errorf("X-Ember-Now = %q, want server Unix seconds", h.Get("X-Ember-Now"))
	}
	if len(body) == 0 {
		t.Fatal("empty view body")
	}
	resp, body = f.get(t, etag)
	h = resp.Header
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 || h.Get("ETag") != etag ||
		h.Get("X-Ember-Now") == "" || h.Get("X-Ember-View-Wait") != "25" {
		t.Fatalf("revalidate = %d body %q headers %v, want 304 with ETag, X-Ember-Now and X-Ember-View-Wait", resp.StatusCode, body, h)
	}
	cr, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, `{"fw":"0.9.13","config_version":1}`)
	mustOK(t, "checkin", cr, b)
	if _, err := strconv.ParseInt(cr.Header.Get("X-Ember-Now"), 10, 64); err != nil {
		t.Errorf("checkin X-Ember-Now = %q", cr.Header.Get("X-Ember-Now"))
	}
}

func TestDevicePomodoroActionGolden(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.RateLimit.Disabled = true })
	clk := &stepClock{now: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}
	app.EnablePomodoro(pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, clk), app.store)
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "POST", "/v1/pomodoro/start", m.Token, `{"phase":"short_break"}`)
	mustOK(t, "start", resp, b)
	resp, b = devReq(t, srv, "POST", "/v1/pomodoro/pause", m.Token, "")
	assertDeviceGolden(t, "pomodoro_action", mustOK(t, "pause", resp, b))
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
		"link_fallback": true, "link_mhz": 80,
		"ota": map[string]any{"image": "valid", "phase": "idle", "rollback": true, "slot": 0,
			"last": map[string]any{"attempt": 2, "error": "bad_checksum", "result": "failed", "version": "0.9.12"}},
		"rssi": -58,
		"stats": map[string]any{
			"cpu_pct": []float64{12.5, 3.5}, "fps": 29.5, "frame_ms_avg": 12.25, "frame_ms_max": 40,
			"heap_internal_min": 30000, "period_ms": 30000, "psram_free": 7000000, "psram_largest": 6000000,
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
	if last == nil {
		t.Fatal("no last checkin stored")
	}
	got := *last
	got.SeenAt = time.Time{}
	if got.Diag != nil {
		d := *got.Diag
		d.Reboots, d.PrevResetReason, d.RebootsSinceSeen = 0, "", 0
		got.Diag = &d
	}
	want := deviceCheckin{
		FW: "0.9.13", IP: "192.0.2.10", RSSI: -58, HeapInternalFree: 47104, HeapInternalLargest: 31744,
		UptimeS: 812, AppliedVersion: 2, LinkMHz: 80, LinkFallback: true,
		Wifi: &deviceWifi{BSSID: "02:00:5e:00:00:01", Channel: 6, Disconnects: 3, LastReason: 203, RSSIMin: -83},
		Diag: &deviceDiag{Boots: 12, HeapInternalMin: 30120, HeapLargestMin: 22528, ResetReason: "panic",
			Crash:     &deviceCrash{ELF: "a1b2c3d4", ID: crashID, Size: len(dump), PC: "0x4201a2b3", Reason: "panic", Task: "ember"},
			StackFree: map[string]int{"ember": 1220, "lvgl": 2048}},
		FWBuild: runningBuild,
		OTA: &knobOTAReport{Image: "valid", Phase: "idle", Rollback: true, Slot: new(0),
			Last: &knobOTALast{Attempt: 2, Error: "bad_checksum", Result: "failed", Version: "0.9.12"}},
	}
	if !reflect.DeepEqual(got, want) {
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		t.Errorf("stored checkin\n got %s\nwant %s", gb, wb)
	}

	_, latest := app.knobStats.points(k.knob.ID, "15m", clk.Now())
	if latest == nil {
		t.Fatal("stats not recorded")
	}
	wantStats := knobSample{
		T: latest.T, BrightnessLevel: latest.BrightnessLevel,
		UptimeSec: new(int64(812)), RSSIDBm: new(-58), CPUPercent: []float64{12.5, 3.5},
		HeapInternalFreeBytes: new(int64(47104)), HeapInternalMinBytes: new(int64(30000)), HeapInternalLargest: new(int64(31744)),
		PSRAMFreeBytes: new(int64(7000000)), PSRAMMinBytes: new(int64(6500000)), PSRAMLargestBytes: new(int64(6000000)),
		TempC: new(41.5), RequestsPerMin: new(60.0), RequestFailuresPerMin: new(4.0),
		RequestLatencyAvgMS: new(35.5), RequestLatencyMaxMS: new(int64(120)),
		RenderFPS: new(29.5), FrameAvgMS: new(12.25), FrameMaxMS: new(int64(40)),
		periodMS: 30000, resetReason: "poweron",
	}
	if !reflect.DeepEqual(*latest, wantStats) {
		t.Errorf("stored stats\n got %+v\nwant %+v", *latest, wantStats)
	}
}

func TestDeviceCheckinRotationGolden(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	resp, b := devReq(t, srv, "POST", "/v1/devices/"+m.ID+"/rotate", testToken, "")
	mustOK(t, "rotate", resp, b)
	resp, b = devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"fw":"0.9.13","config_version":1}`)
	reply := mustOK(t, "checkin", resp, b)
	var r struct {
		NewToken string `json:"new_token"`
	}
	if err := json.Unmarshal(reply, &r); err != nil || !strings.HasPrefix(r.NewToken, "ekd_") || len(r.NewToken) != len(exampleDeviceToken) {
		t.Fatalf("rotation reply = %s", reply)
	}
	assertDeviceGolden(t, "checkin_reply_rotation", bytes.ReplaceAll(reply, []byte(r.NewToken), []byte(exampleDeviceToken)))
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
