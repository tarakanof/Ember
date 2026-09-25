package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// getOpen issues an unauthenticated GET: the dashboard read endpoints must
// answer without the bearer token, like /state and the preview endpoints.
func getOpen(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// assertISOSeconds fails unless v is an RFC 3339 timestamp without fractional
// seconds: Swift's JSONDecoder .iso8601 strategy rejects "…:05.123+02:00".
func assertISOSeconds(t *testing.T, field string, v any) {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s = %v (%T), want an ISO 8601 string", field, v, v)
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil || strings.Contains(s, ".") {
		t.Errorf("%s = %q, want RFC 3339 with whole seconds", field, s)
	}
}

func TestUsageSnapshotIsReadableWithoutToken(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	post := `{"tool":"claude","source":"m4",
	  "five_hour":{"used_percent":14,"resets_at":1790389200,"reset_label":"04:20"},
	  "seven_day":{"used_percent":37.5},
	  "models":{"sonnet":{"used_percent":20},"opus":{"used_percent":5}}}`
	if resp, _ := doReq(t, srv, http.MethodPost, "/v1/usage", "", post); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /v1/usage = %d", resp.StatusCode)
	}

	status, body := getOpen(t, srv, "/v1/usage")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/usage = %d, want 200", status)
	}
	assertISOSeconds(t, "generated_at", body["generated_at"])
	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one entry", tools)
	}
	claude := tools[0].(map[string]any)
	if claude["tool"] != "claude" || claude["source"] != "m4" || claude["stale"] != false {
		t.Errorf("tool entry = %v", claude)
	}
	assertISOSeconds(t, "updated_at", claude["updated_at"])
	five := claude["five_hour"].(map[string]any)
	if five["used_percent"] != 14.0 || five["reset_label"] != "04:20" {
		t.Errorf("five_hour = %v", five)
	}
	assertISOSeconds(t, "five_hour.resets_at", five["resets_at"])
	if got, _ := time.Parse(time.RFC3339, five["resets_at"].(string)); got.Unix() != 1790389200 {
		t.Errorf("resets_at = %v, want unix 1790389200", five["resets_at"])
	}
	if seven := claude["seven_day"].(map[string]any); seven["resets_at"] != nil {
		t.Errorf("seven_day.resets_at = %v, want null when the producer sent none", seven["resets_at"])
	}
	models := claude["models"].([]any)
	if len(models) != 2 || models[0].(map[string]any)["model"] != "opus" || models[1].(map[string]any)["model"] != "sonnet" {
		t.Errorf("models = %v, want [opus sonnet] sorted by name", models)
	}
}

func TestUsageSnapshotEmptyIsEmptyArray(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/usage")
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want []", body["tools"])
	}
}

func TestActivitySummaryGroupsByToolAndSource(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// Anchor the rows at the start of today's logical day so they never spill
	// into yesterday, whatever time the test runs.
	p := app.cfg.Load().Pomodoro
	now := time.Now()
	start := logicalDayStart(now, p.DayStartHour, time.Local)
	if now.Sub(start) < 30*time.Minute {
		t.Skip("too close to the day boundary for a 20-minute fixture")
	}
	record := func(at time.Time, source, tool, session, state string) {
		t.Helper()
		if err := app.store.RecordActivity(at, source, tool, session, state); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= 10; i++ { // 20 minutes of claude on m4, one waiting episode
		state := "running"
		if i == 5 {
			state = "waiting"
		}
		record(start.Add(time.Duration(2*i)*time.Minute), "m4", "claude", "m4/claude/s1", state)
	}
	for i := 0; i <= 5; i++ { // 10 minutes of codex on m5
		record(start.Add(time.Duration(2*i)*time.Minute), "m5", "codex", "m5/codex/s2", "running")
	}

	status, body := getOpen(t, srv, "/v1/activity/summary?days=3")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/activity/summary = %d, want 200", status)
	}
	if body["days"] != 3.0 || body["recording"] != true {
		t.Errorf("days/recording = %v/%v", body["days"], body["recording"])
	}
	today := body["today"].(map[string]any)
	assertISOSeconds(t, "today.from", today["from"])
	total := today["total"].(map[string]any)
	if total["active_sec"] != 1200.0 || total["sessions"] != 2.0 || total["attention"] != 1.0 {
		t.Errorf("today.total = %v, want 1200s (union), 2 sessions, 1 attention", total)
	}
	byTool := map[string]map[string]any{}
	for _, g := range today["by_tool"].([]any) {
		m := g.(map[string]any)
		byTool[m["key"].(string)] = m
	}
	if byTool["claude"]["active_sec"] != 1200.0 || byTool["codex"]["active_sec"] != 600.0 {
		t.Errorf("by_tool = %v", byTool)
	}
	bySource := today["by_source"].([]any)
	if len(bySource) != 2 || bySource[0].(map[string]any)["key"] != "m4" {
		t.Errorf("by_source = %v, want m4 first (most active)", bySource)
	}

	// daily is zero-filled: every day of the window for every tool seen,
	// oldest first, so a stacked bar chart has a continuous x axis.
	daily := body["daily"].([]any)
	if len(daily) != 3*2 {
		t.Fatalf("daily has %d points, want 6 (3 days × 2 tools)", len(daily))
	}
	first, last := daily[0].(map[string]any), daily[len(daily)-1].(map[string]any)
	assertISOSeconds(t, "daily[0].date", first["date"])
	if first["active_sec"] != 0.0 {
		t.Errorf("oldest day = %v, want zero-filled", first)
	}
	if last["day"] != logicalDayKey(now, p.DayStartHour, time.Local) {
		t.Errorf("newest point day = %v, want today", last["day"])
	}
}

func TestActivitySummaryWithoutStoreIs404(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if status, _ := getOpen(t, srv, "/v1/activity/summary"); status != http.StatusNotFound {
		t.Errorf("status = %d, want 404 without a store", status)
	}
}

func TestWeatherStateBeforeFirstFetch(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	status, body := getOpen(t, srv, "/v1/weather/state")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["current"] != nil || body["air"] != nil {
		t.Errorf("current/air = %v/%v, want null before any fetch", body["current"], body["air"])
	}
}

func TestWeatherStateReportsCachedObservation(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) {
		c.Weather.Enabled = true
		c.Weather.Latitude, c.Weather.Longitude = 52.37, 4.9
	})
	fetched := time.Now().Add(-5 * time.Minute)
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: "rain", TempC: 11.5, FetchedAt: fetched, Hourly: []float64{11.5, 12, 12.5}}
	app.weather.have = true
	app.weather.air = airObservation{AQI: 42, PM25: 8, PM10: 15, HourlyAQI: []float64{42, 40}, FetchedAt: fetched}
	app.weather.haveAir = true
	app.weather.mu.Unlock()
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/weather/state")
	if _, leaked := body["latitude"]; leaked {
		t.Errorf("weather state must not expose the home location: %v", body)
	}
	cur := body["current"].(map[string]any)
	if cur["condition"] != "rain" || cur["temp_c"] != 11.5 || cur["stale"] != false {
		t.Errorf("current = %v", cur)
	}
	assertISOSeconds(t, "current.fetched_at", cur["fetched_at"])
	hourly := cur["hourly"].([]any)
	if len(hourly) != 3 {
		t.Fatalf("hourly = %v", hourly)
	}
	h0, h1 := hourly[0].(map[string]any), hourly[1].(map[string]any)
	assertISOSeconds(t, "hourly[0].time", h0["time"])
	t0, _ := time.Parse(time.RFC3339, h0["time"].(string))
	t1, _ := time.Parse(time.RFC3339, h1["time"].(string))
	if t1.Sub(t0) != time.Hour || h1["temp_c"] != 12.0 {
		t.Errorf("hourly points = %v, %v; want one hour apart", h0, h1)
	}
	air := body["air"].(map[string]any)
	if air["european_aqi"] != 42.0 || air["pm2_5_ugm3"] != 8.0 || len(air["hourly"].([]any)) != 2 {
		t.Errorf("air = %v", air)
	}
	sun := body["sun"].(map[string]any)
	assertISOSeconds(t, "sun.sunrise", sun["sunrise"])
	assertISOSeconds(t, "sun.sunset", sun["sunset"])
}

// ngHealthClock serves GET /api/v1/device like awtrix-ng 1.1.2 and counts hits.
func ngHealthClock(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(`{"version":"1.1.2","wifiRssi":-71,"uptimeSeconds":268719,
		  "freeHeapBytes":103032,"minFreeHeapBytes":76544,"resetReason":"software","fps":42,
		  "batteryPercent":97,"temperature":33.4,"humidity":21.4,
		  "wifi":{"state":"connected","connects":3}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClockHealthReportsPublishAndDeviceTelemetry(t *testing.T) {
	var hits atomic.Int32
	clock := ngHealthClock(t, &hits)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	for range 3 {
		app.metrics.incPublishOK()
	}
	app.metrics.incPublishFail()
	app.metrics.incPublishRetry()
	app.recordPublish(Snapshot{}, nil)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	status, body := getOpen(t, srv, "/v1/clock/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	pub := body["publish"].(map[string]any)
	if pub["ok_total"] != 3.0 || pub["fail_total"] != 1.0 || pub["retries_total"] != 1.0 ||
		pub["success_ratio"] != 0.75 || pub["last_ok"] != true {
		t.Errorf("publish = %v", pub)
	}
	assertISOSeconds(t, "publish.last_at", pub["last_at"])
	assertISOSeconds(t, "publish.counting_since", pub["counting_since"])
	dev := body["device"].(map[string]any)
	if dev["reachable"] != true || dev["wifi_rssi_dbm"] != -71.0 || dev["free_heap_bytes"] != 103032.0 ||
		dev["uptime_sec"] != 268719.0 || dev["firmware"] != "1.1.2" || dev["wifi_connects"] != 3.0 ||
		dev["temperature_c"] != 33.4 {
		t.Errorf("device = %v", dev)
	}
	assertISOSeconds(t, "device.checked_at", dev["checked_at"])
}

// The endpoint is unauthenticated and the clock sits on lossy Wi-Fi, so a
// dashboard (or anyone) polling it must not turn into a probe per request.
func TestClockHealthCachesTheDeviceProbe(t *testing.T) {
	var hits atomic.Int32
	clock := ngHealthClock(t, &hits)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	for range 3 {
		getOpen(t, srv, "/v1/clock/health")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("clock probed %d times for 3 requests, want 1 (cached)", n)
	}
}

func TestClockHealthUnreachableClockStillAnswers(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = deadURL })
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	status, body := getOpen(t, srv, "/v1/clock/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 with reachable=false", status)
	}
	dev := body["device"].(map[string]any)
	if dev["reachable"] != false || dev["wifi_rssi_dbm"] != nil {
		t.Errorf("device = %v, want unreachable with null telemetry", dev)
	}
	if pub := body["publish"].(map[string]any); pub["success_ratio"] != nil || pub["last_at"] != nil {
		t.Errorf("publish = %v, want null ratio/last_at before any publish", pub)
	}
}
