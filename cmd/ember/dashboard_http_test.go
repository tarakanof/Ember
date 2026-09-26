package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/dashboard golden files")

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

// ngHealthClock serves GET /api/v1/device like awtrix-ng 1.1.2 and counts hits.
func ngHealthClock(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(`{"version":"1.1.1","ipAddress":"192.0.2.66","uid":"e868e705ffb8","hostname":"Awtrix",
		  "wifiRssi":-71,"uptimeSeconds":268719,"freeHeapBytes":103032,"minFreeHeapBytes":76544,
		  "resetReason":"software","fps":42,"matrixPower":true,"batteryPercent":97,"lowBattery":false,
		  "temperature":33.4,"humidity":21.4,"currentApp":"Time",
		  "wifi":{"state":"connected","host":"HomeNet","connects":3}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// closedURL is a URL nothing listens on. Tests point the clock at it so a
// health probe never reaches defaultDeviceBaseURL on the developer's LAN.
func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

// ngReleases serves GitHub's latest-release payload for awtrix-ng.
func ngReleases(t *testing.T, tag string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "user agent required", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","prerelease":false}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---- golden files: the wire contract EmberKit's decode tests read too ----

// goldenZone and goldenNow pin every builder input so the files are stable on
// any machine: 2026-09-26 10:30:00 +02:00.
var (
	goldenZone = time.FixedZone("CEST", 2*3600)
	goldenNow  = time.Date(2026, 9, 26, 10, 30, 0, 0, goldenZone)
)

// assertGolden compares v, marshalled as indented JSON, with
// testdata/dashboard/<name>.json; -update rewrites the file instead.
func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "dashboard", name+".json")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test -run TestDashboardGolden -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s drifted from %s (run with -update if intended)\ngot:\n%s", name, path, got)
	}
}

// goldenApp is a Pomodoro-enabled app populated with fixed dashboard data.
func goldenApp(t *testing.T) *App {
	t.Helper()
	app := newPomodoroApp(t)
	app.startedAt = goldenNow.Add(-26 * time.Hour)
	dead := closedURL(t)
	app.updateConfig(func(c *Config) {
		c.AWTRIX.HTTPBaseURL = dead
		c.Pomodoro.DayStartHour = 4
		c.Weather.Enabled = true
		c.Weather.Provider = "open-meteo"
		c.Weather.Units = "metric"
		c.Weather.LocationName = "Amsterdam"
		c.Weather.Latitude, c.Weather.Longitude = 52.37, 4.9
	})

	app.usage.Put("claude", ToolUsage{
		Source:    "m4",
		FiveHour:  &UsageWindow{UsedPercent: 14, ResetsAt: goldenNow.Add(2 * time.Hour).Unix(), ResetLabel: "12:30"},
		SevenDay:  &UsageWindow{UsedPercent: 37.5},
		Models:    map[string]*UsageWindow{"opus": {UsedPercent: 5}, "sonnet": {UsedPercent: 20.5}},
		UpdatedAt: goldenNow.Add(-2 * time.Minute),
	})
	app.usage.Put("codex", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 3},
		UpdatedAt: goldenNow.Add(-20 * time.Minute),
	})

	// Today (logical day from 04:00): claude on m4 08:00-08:12, waiting
	// 08:04-08:08; codex on m5 09:00-09:10. Yesterday: claude on m5 for 30 min.
	record := func(at time.Time, source, tool, session, state string) {
		t.Helper()
		if err := app.store.RecordActivity(at, source, tool, session, state); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 9, 26, 8, 0, 0, 0, goldenZone)
	for i, st := range []string{"running", "running", "waiting", "waiting", "waiting", "running", "running"} {
		record(day.Add(time.Duration(2*i)*time.Minute), "m4", "claude", "m4/claude/s1", st)
	}
	for i := 0; i <= 5; i++ {
		record(day.Add(time.Hour+time.Duration(2*i)*time.Minute), "m5", "codex", "m5/codex/s2", "running")
	}
	for i := 0; i <= 15; i++ {
		record(day.AddDate(0, 0, -1).Add(time.Duration(2*i)*time.Minute), "m5", "claude", "m5/claude/s3", "running")
	}
	orange, teal := "#FF8800", "#00C8C8"
	app.sourceColors.remember("m4", &teal)
	app.sourceColors.remember("m5", &orange)

	fetched := goldenNow.Add(-4 * time.Minute)
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{
		Condition: "rain", ConditionCode: "61", TempC: 11.5, FetchedAt: fetched,
		Hourly: []float64{11.5, 12, 12.5}, HourlyStart: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC),
	}
	app.weather.have = true
	app.weather.air = airObservation{
		AQI: 42, PM25: 8, PM10: 15, FetchedAt: fetched,
		HourlyAQI: []float64{42, 40}, HourlyStart: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC),
	}
	app.weather.haveAir = true
	app.weather.mu.Unlock()
	return app
}

func TestDashboardGolden(t *testing.T) {
	var clockHits, releaseHits atomic.Int32
	app := goldenApp(t)
	clock := ngHealthClock(t, &clockHits)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.firmware.url = ngReleases(t, "v1.1.2", &releaseHits).URL
	for range 3 {
		app.metrics.incPublishOK()
	}
	app.metrics.incPublishFail()
	app.metrics.incPublishRetry()
	app.publishWindow.add(goldenNow.Add(-30*time.Hour), false) // outside the 24h window
	app.publishWindow.add(goldenNow.Add(-2*time.Hour), true)
	app.publishWindow.add(goldenNow.Add(-2*time.Hour), true)
	app.publishWindow.add(goldenNow.Add(-1*time.Hour), false)
	app.publishWindow.add(goldenNow.Add(-1*time.Minute), true)
	app.mu.Lock()
	app.lastPublishAt, app.lastPublishOK = goldenNow.Add(-time.Minute).UTC(), true
	app.mu.Unlock()

	assertGolden(t, "usage", app.buildUsageSnapshot(goldenNow))
	summary, err := app.buildActivitySummary(goldenNow, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "activity_summary", summary)
	assertGolden(t, "weather_state", app.buildWeatherState(goldenNow))
	assertGolden(t, "clock_health", app.buildClockHealth(t.Context(), goldenNow))

	empty := newPomodoroApp(t)
	empty.startedAt = goldenNow.Add(-time.Minute)
	assertGolden(t, "weather_state_empty", empty.buildWeatherState(goldenNow))
	dead := closedURL(t)
	empty.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = dead })
	assertGolden(t, "clock_health_unreachable", empty.buildClockHealth(t.Context(), goldenNow))
}

// ---- behaviour through the real routes ----

func TestDashboardEndpointsAreOpenAndWholeSecond(t *testing.T) {
	app := goldenApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	for _, path := range []string{"/v1/usage", "/v1/activity/summary", "/v1/weather/state", "/v1/clock/health"} {
		status, body := getOpen(t, srv, path)
		if status != http.StatusOK {
			t.Errorf("GET %s without token = %d, want 200", path, status)
			continue
		}
		assertISOSeconds(t, path+" generated_at", body["generated_at"])
	}
}

func TestUsageSnapshotPostStaysAuthed(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/v1/usage", "application/json", strings.NewReader(`{"tool":"claude"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /v1/usage without token = %d, want 401", resp.StatusCode)
	}
	_, body := getOpen(t, srv, "/v1/usage")
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want []", body["tools"])
	}
}

func TestActivitySummaryExcludesWaitingFromActiveTime(t *testing.T) {
	app := goldenApp(t)
	out, err := app.buildActivitySummary(goldenNow, 2)
	if err != nil {
		t.Fatal(err)
	}
	// claude on m4 runs 08:00-08:02, waits 08:04-08:08, runs 08:10-08:12. With
	// the waiting rows out, the running rows are 8 min apart (> the 5-min span
	// gap): two 2-minute spans and one attention episode.
	var claude activityTotalsOut
	for _, g := range out.Today.ByTool {
		if g.Key == "claude" {
			claude = g
		}
	}
	if claude.ActiveSec != 4*60 || claude.Attention != 1 || claude.Sessions != 1 {
		t.Errorf("today claude = %+v, want 240s active, 1 attention, 1 session", claude)
	}
	if len(out.DailyBySource) != 2*2 {
		t.Fatalf("daily_by_source = %d points, want 4 (2 days × 2 sources)", len(out.DailyBySource))
	}
	if c := out.DailyBySource[0].SourceColor; c == nil || *c != "#00C8C8" {
		t.Errorf("m4 colour = %v, want #00C8C8", c)
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

func TestActivitySummaryHidesStorageErrors(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	_ = app.store.Close()

	status, body := getOpen(t, srv, "/v1/activity/summary")
	if status != http.StatusInternalServerError || body["error"] != "internal error" {
		t.Errorf("status/body = %d %v, want 500 with a generic message", status, body)
	}
}

func TestWeatherStateRoundsSunTimes(t *testing.T) {
	out := goldenApp(t).buildWeatherState(goldenNow)
	if out.Sun == nil {
		t.Fatal("sun = nil, want times for Amsterdam")
	}
	for name, v := range map[string]time.Time{"sunrise": out.Sun.Sunrise, "sunset": out.Sun.Sunset} {
		if v.Unix()%int64(sunRounding/time.Second) != 0 {
			t.Errorf("%s = %v, want a multiple of %v", name, v, sunRounding)
		}
	}
}

func TestWeatherStateHourlyNeedsAStampedStart(t *testing.T) {
	app := newPomodoroApp(t)
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: "clear", FetchedAt: goldenNow, Hourly: []float64{1, 2}}
	app.weather.have = true
	app.weather.mu.Unlock()

	out := app.buildWeatherState(goldenNow)
	if out.Current == nil || len(out.Current.Hourly) != 0 {
		t.Errorf("hourly = %v, want [] when the provider didn't stamp the start", out.Current)
	}
}

func TestWeatherFetchersStampHourlyStartAndCode(t *testing.T) {
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("timeformat") != "unixtime" {
			t.Errorf("open-meteo timeformat = %q, want unixtime", r.URL.Query().Get("timeformat"))
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":9,"weather_code":61},"hourly":{"time":[1790409600,1790413200],"temperature_2m":[9,10]}}`))
	}))
	defer om.Close()
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"properties":{"timeseries":[{"time":"2026-09-26T07:00:00Z","data":{"instant":{"details":{"air_temperature":8}},"next_1_hours":{"summary":{"symbol_code":"rain_showers_day"}}}}]}}`))
	}))
	defer met.Close()
	wf := newWeatherFetcher()
	wf.openMeteoBase, wf.metNoBase = om.URL, met.URL

	obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 1, Longitude: 2})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ConditionCode != "61" || obs.HourlyStart.Unix() != 1790409600 {
		t.Errorf("open-meteo code/start = %q/%v", obs.ConditionCode, obs.HourlyStart)
	}
	obs, err = wf.fetchMetNo(t.Context(), WeatherConfig{Latitude: 1, Longitude: 2})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ConditionCode != "rain_showers_day" || !obs.HourlyStart.Equal(time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("met-no code/start = %q/%v", obs.ConditionCode, obs.HourlyStart)
	}
}

func TestClockHealthDropsIdentifyingFields(t *testing.T) {
	var hits atomic.Int32
	app := newPomodoroApp(t)
	clock := ngHealthClock(t, &hits)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.lastButtonAt.Store(goldenNow.Unix())
	b, err := json.Marshal(app.buildClockHealth(t.Context(), goldenNow))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"192.0.2.66", "e868e705ffb8", "HomeNet", "Awtrix", "last_button", clock.URL} {
		if strings.Contains(string(b), leak) {
			t.Errorf("clock health leaks %q: %s", leak, b)
		}
	}
}

// The endpoint is open and the clock sits on lossy Wi-Fi, so polling must not
// turn into a probe per request.
func TestClockHealthCachesTheDeviceProbe(t *testing.T) {
	var hits, releases atomic.Int32
	clock := ngHealthClock(t, &hits)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.firmware.url = ngReleases(t, "v1.1.2", &releases).URL
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	for range 3 {
		getOpen(t, srv, "/v1/clock/health")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("clock probed %d times for 3 requests, want 1 (cached)", n)
	}
	if n := releases.Load(); n != 1 {
		t.Errorf("release lookups = %d for 3 requests, want 1 (cached)", n)
	}
}

// A viewer that disconnects mid-probe must not cache "unreachable" for
// everyone else for the next 30s.
func TestClockHealthProbeIgnoresCallerCancellation(t *testing.T) {
	var hits atomic.Int32
	clock := ngHealthClock(t, &hits)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if dev := app.probeClockHealth(ctx, time.Now()); dev == nil || !dev.Reachable {
		t.Errorf("probe with a cancelled caller = %+v, want reachable", dev)
	}
}

func TestClockHealthFirmwareLookupFailsSoft(t *testing.T) {
	var hits atomic.Int32
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer down.Close()
	app := newPomodoroApp(t)
	dead := closedURL(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = dead })
	app.firmware.url = down.URL

	out := app.buildClockHealth(t.Context(), goldenNow)
	if out.LatestFirmware != nil || out.UpdateAvailable != nil {
		t.Errorf("latest/update = %v/%v, want null on a failed lookup", out.LatestFirmware, out.UpdateAvailable)
	}
	app.buildClockHealth(t.Context(), goldenNow.Add(time.Minute))
	if n := hits.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1 (a failure backs off %v)", n, firmwareCheckRetry)
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		latest, installed string
		newer, ok         bool
	}{
		{"1.1.2", "1.1.1", true, true},
		{"1.1.2", "1.1.2", false, true},
		{"1.2", "1.1.9", true, true},
		{"1.1.10", "1.1.9", true, true},
		{"1.1.2", "1.1.2.1", false, true},
		{"1.1.2", "dev", false, false},
	}
	for _, c := range cases {
		newer, ok := versionNewer(c.latest, c.installed)
		if newer != c.newer || ok != c.ok {
			t.Errorf("versionNewer(%q, %q) = %v, %v; want %v, %v", c.latest, c.installed, newer, ok, c.newer, c.ok)
		}
	}
}
