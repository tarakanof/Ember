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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

var updateGolden = flag.Bool("update", false, "rewrite the testdata golden files")

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
		  "temperature":33.4,"humidity":21.4,"lightLevel":42.5,"currentApp":"Time",
		  "wifi":{"state":"connected","host":"HomeNet","connects":3}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

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

var (
	goldenZone = time.FixedZone("CEST", 2*3600)
	goldenNow  = time.Date(2026, 9, 26, 10, 30, 0, 0, goldenZone)
)

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	compareGolden(t, filepath.Join("testdata", "dashboard", name+".json"), got)
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
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
		t.Fatalf("read golden (run go test ./cmd/ember -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s drifted (run with -update if intended)\ngot:\n%s", path, got)
	}
}

func goldenPrecip() []weather.PrecipStep {
	start := time.Date(2026, 9, 26, 10, 15, 0, 0, goldenZone)
	mmh := []float64{3.1, 1.2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8.0, 0}
	out := make([]weather.PrecipStep, len(mmh))
	for i, v := range mmh {
		out[i] = weather.PrecipStep{At: start.Add(time.Duration(i) * 15 * time.Minute), MMh: v}
	}
	return out
}

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

	putUsage(t, app.usage, "claude", ToolUsage{
		Source:    "m4",
		FiveHour:  &UsageWindow{UsedPercent: 14, ResetsAt: goldenNow.Add(2 * time.Hour).Unix(), ResetLabel: "12:30"},
		SevenDay:  &UsageWindow{UsedPercent: 37.5},
		Models:    map[string]*UsageWindow{"opus": {UsedPercent: 5}, "sonnet": {UsedPercent: 20.5}},
		UpdatedAt: goldenNow.Add(-2 * time.Minute),
	})
	putUsage(t, app.usage, "codex", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 3},
		UpdatedAt: goldenNow.Add(-20 * time.Minute),
	})

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
	app.weather.obs = weather.Observation{
		Condition: "rain", ConditionCode: "61", TempC: 11.5, FetchedAt: fetched, TZKnown: true, TZOffsetSeconds: 7200,
		Now:    weather.Now{Cloud: new(62.4), UV: new(2.6), WindKmh: new(18.4), GustKmh: new(31.2), WindDir: new(242.6)},
		Hourly: []float64{11.5, 12, 12.5}, HourlyStart: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC),
		Days: []weather.Day{
			{Start: time.Date(2026, 9, 26, 0, 0, 0, 0, goldenZone), MinC: new(8.4), MaxC: new(16.6), Pop: new(80.0)},
			{Start: time.Date(2026, 9, 27, 0, 0, 0, 0, goldenZone), Cond: "rain", Code: "63", MinC: new(9.6), MaxC: new(14.4), Pop: new(70.0)},
			{Start: time.Date(2026, 9, 28, 0, 0, 0, 0, goldenZone), Cond: "clear", Code: "0", MinC: new(7.2), MaxC: new(18.5), Pop: new(5.0)},
		},
		PrecipStep: 15 * time.Minute,
		Precip:     goldenPrecip(),
	}
	app.weather.have = true
	app.weather.source = app.cfg.Load().Weather.Source()
	app.weather.air = weather.AirObservation{
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
	app.firmware.cached(goldenNow, nil)
	app.firmware.wg.Wait()
	for range 3 {
		app.metrics.incPublishOK()
	}
	app.metrics.incPublishFail()
	app.metrics.incPublishRetry()
	app.publishWindow.add(goldenNow.Add(-30*time.Hour), false)
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

	t.Setenv("EMBER_CLOCK", "off")
	assertGolden(t, "clock_health_disabled", empty.buildClockHealth(t.Context(), goldenNow))
}

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

func TestActivitySummaryUnknownSourceColorIsExplicitNull(t *testing.T) {
	app := newPomodoroApp(t)
	start := logicalDayStart(goldenNow, app.cfg.Load().Pomodoro.DayStartHour, goldenZone).Add(time.Hour)
	for i := 0; i <= 3; i++ {
		if err := app.store.RecordActivity(start.Add(time.Duration(2*i)*time.Minute), "ci", "codex", "ci/codex/s1", "running"); err != nil {
			t.Fatal(err)
		}
	}
	out, err := app.buildActivitySummary(goldenNow, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Today struct {
			BySource []map[string]any `json:"by_source"`
		} `json:"today"`
		DailyBySource []map[string]any `json:"daily_by_source"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for name, rows := range map[string][]map[string]any{"by_source": wire.Today.BySource, "daily_by_source": wire.DailyBySource} {
		if len(rows) != 1 {
			t.Fatalf("%s = %v, want one row", name, rows)
		}
		if v, present := rows[0]["source_color"]; !present || v != nil {
			t.Errorf("%s source_color = %v (present=%v), want explicit null", name, v, present)
		}
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

func TestWeatherStateTodayRangeIsTheKnobsRange(t *testing.T) {
	out := goldenApp(t).buildWeatherState(goldenNow)
	if out.Current == nil || out.Current.TodayMinC == nil || out.Current.TodayMaxC == nil {
		t.Fatalf("today range = %+v, want the forecast day's min and max", out.Current)
	}
	if *out.Current.TodayMinC != 8.4 || *out.Current.TodayMaxC != 16.6 {
		t.Errorf("today = %v..%v, want 8.4..16.6", *out.Current.TodayMinC, *out.Current.TodayMaxC)
	}

	app := newPomodoroApp(t)
	app.weather.mu.Lock()
	app.weather.obs = weather.Observation{Condition: "clear", TempC: 21, FetchedAt: goldenNow,
		Days: []weather.Day{{Start: goldenNow.Add(-time.Hour), MinC: new(9.0), MaxC: new(18.0)}}}
	app.weather.have = true
	app.weather.source = app.cfg.Load().Weather.Source()
	app.weather.mu.Unlock()
	cur := app.buildWeatherState(goldenNow).Current
	if cur == nil || cur.TodayMaxC == nil || *cur.TodayMaxC != 21 {
		t.Errorf("today max = %+v, want the current 21 C above the forecast max", cur)
	}
	app.weather.mu.Lock()
	app.weather.obs.Days = nil
	app.weather.mu.Unlock()
	if cur := app.buildWeatherState(goldenNow).Current; cur == nil || cur.TodayMinC != nil || cur.TodayMaxC != nil {
		t.Errorf("today = %+v, want null without a forecast day", cur)
	}
}

func TestWeatherStateCloudAndUVAreTheKnobViews(t *testing.T) {
	app := goldenApp(t)
	cfg := app.cfg.Load().Weather
	cur := app.buildWeatherState(goldenNow).Current
	view := newKnobWeatherDetail(app.weather.state(cfg, goldenNow), cfg)
	if cur == nil || view.Now == nil || cur.CloudPct == nil || cur.UV == nil {
		t.Fatalf("current = %+v, knob now = %+v, want cloud and UV in both", cur, view.Now)
	}
	if *cur.CloudPct != *view.Now.Cloud || *cur.UV != *view.Now.UV || *cur.CloudPct != 62 || *cur.UV != 3 {
		t.Errorf("cloud_pct %d uv %d, knob view cloud %d uv %d, want 62 and 3 in both", *cur.CloudPct, *cur.UV, *view.Now.Cloud, *view.Now.UV)
	}

	app.weather.mu.Lock()
	app.weather.obs.Now = weather.Now{}
	app.weather.mu.Unlock()
	raw, err := json.Marshal(app.buildWeatherState(goldenNow).Current)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cloud_pct", "uv"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null when the provider has none", k, v, ok)
		}
	}
}

func TestWeatherStateCloudAndUVParityEdges(t *testing.T) {
	type pair struct{ cloud, uv *int }
	for _, tc := range []struct {
		name      string
		now       weather.Now
		want      pair
		wantKnobs bool
	}{
		{"clear sky, no UV", weather.Now{Cloud: new(0.0)}, pair{cloud: new(0)}, true},
		{"no cloud, UV 0 at night", weather.Now{UV: new(0.0)}, pair{uv: new(0)}, true},
		{"halves round away from zero", weather.Now{Cloud: new(62.5), UV: new(2.5)}, pair{new(63), new(3)}, true},
		{"just under a half", weather.Now{Cloud: new(9.49), UV: new(2.49)}, pair{new(9), new(2)}, true},
		{"range tops", weather.Now{Cloud: new(100.0), UV: new(40.0)}, pair{new(100), new(40)}, true},
		{"tiny negative rounds to zero", weather.Now{Cloud: new(-0.4), UV: new(-0.4)}, pair{new(0), new(0)}, true},
		{"neither", weather.Now{}, pair{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := goldenApp(t)
			cfg := app.cfg.Load().Weather
			app.weather.mu.Lock()
			app.weather.obs.Now = tc.now
			app.weather.obs.Precip = nil
			app.weather.mu.Unlock()
			cur := app.buildWeatherState(goldenNow).Current
			view := newKnobWeatherDetail(app.weather.state(cfg, goldenNow), cfg)
			eq := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
			show := func(p *int) string {
				if p == nil {
					return "null"
				}
				return strconv.Itoa(*p)
			}
			if !eq(cur.CloudPct, tc.want.cloud) || !eq(cur.UV, tc.want.uv) {
				t.Errorf("cloud_pct %s uv %s, want %s %s", show(cur.CloudPct), show(cur.UV), show(tc.want.cloud), show(tc.want.uv))
			}
			var vc, vu *int
			if view.Now != nil {
				vc, vu = view.Now.Cloud, view.Now.UV
			}
			if !eq(cur.CloudPct, vc) || !eq(cur.UV, vu) {
				t.Errorf("state cloud %s uv %s, knob view cloud %s uv %s", show(cur.CloudPct), show(cur.UV), show(vc), show(vu))
			}
			if (view.Now != nil) != tc.wantKnobs {
				t.Errorf("knob view now = %+v, want present %v", view.Now, tc.wantKnobs)
			}
		})
	}
}

func TestWeatherStateSunNightIsTheKnobViews(t *testing.T) {
	app := goldenApp(t)
	for _, at := range []time.Time{goldenNow, goldenNow.Add(11 * time.Hour)} {
		out := app.buildWeatherState(at)
		knob := app.knobWeather(at)
		if out.Sun == nil || knob == nil || knob.Sunrise == nil {
			t.Fatalf("sun = %+v, knob = %+v, want both with sun times", out.Sun, knob)
		}
		if out.Sun.Night != knob.Night {
			t.Errorf("%v: sun.night %v, knob view night %v", at, out.Sun.Night, knob.Night)
		}
	}
	if !app.buildWeatherState(goldenNow.Add(11*time.Hour)).Sun.Night || app.buildWeatherState(goldenNow).Sun.Night {
		t.Error("want day at 10:30 and night at 21:30 in Amsterdam")
	}

	exact := app.weather.state(app.cfg.Load().Weather, goldenNow).Sun.Set
	for _, tc := range []struct {
		at    time.Time
		night bool
	}{{exact.Add(-30 * time.Second), false}, {exact.Add(30 * time.Second), true}} {
		out := app.buildWeatherState(tc.at)
		if out.Sun.Night != tc.night || out.Sun.Night != app.knobWeather(tc.at).Night {
			t.Errorf("%v (exact sunset %v, sent %v): night %v, want %v like the knob view", tc.at, exact, out.Sun.Sunset, out.Sun.Night, tc.night)
		}
	}
}

func TestWeatherStateHourlyNeedsAStampedStart(t *testing.T) {
	app := newPomodoroApp(t)
	app.weather.mu.Lock()
	app.weather.obs = weather.Observation{Condition: "clear", FetchedAt: goldenNow, Hourly: []float64{1, 2}}
	app.weather.have = true
	app.weather.source = app.cfg.Load().Weather.Source()
	app.weather.mu.Unlock()

	out := app.buildWeatherState(goldenNow)
	if out.Current == nil || len(out.Current.Hourly) != 0 {
		t.Errorf("hourly = %v, want [] when the provider didn't stamp the start", out.Current)
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
	app.firmware.wg.Wait()
	if n := hits.Load(); n != 1 {
		t.Errorf("clock probed %d times for 3 requests, want 1 (cached)", n)
	}
	if n := releases.Load(); n != 1 {
		t.Errorf("release lookups = %d for 3 requests, want 1 (cached)", n)
	}
}

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

	app.buildClockHealth(t.Context(), goldenNow)
	app.firmware.wg.Wait()
	out := app.buildClockHealth(t.Context(), goldenNow.Add(time.Minute))
	app.firmware.wg.Wait()
	if out.LatestFirmware != nil || out.UpdateAvailable != nil {
		t.Errorf("latest/update = %v/%v, want null on a failed lookup", out.LatestFirmware, out.UpdateAvailable)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1 (a failure backs off %v)", n, firmwareCheckRetry)
	}
}

func TestClockHealthNeverWaitsOnTheReleaseLookup(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"tag_name":"v1.1.2"}`))
	}))
	defer slow.Close()
	app := newPomodoroApp(t)
	dead := closedURL(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = dead })
	app.firmware.url = slow.URL

	if out := app.buildClockHealth(t.Context(), goldenNow); out.LatestFirmware != nil {
		t.Errorf("latest_firmware = %v before the lookup finished, want null", *out.LatestFirmware)
	}
	close(release)
	app.firmware.wg.Wait()
	out := app.buildClockHealth(t.Context(), goldenNow.Add(time.Minute))
	if out.LatestFirmware == nil || *out.LatestFirmware != "1.1.2" {
		t.Errorf("latest_firmware = %v after the lookup, want 1.1.2", out.LatestFirmware)
	}
}

func TestClockHealthFirmwareCheckDisabledMakesNoCall(t *testing.T) {
	app := newPomodoroApp(t)
	dead := closedURL(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = dead })
	out := app.buildClockHealth(t.Context(), goldenNow)
	app.firmware.wg.Wait()
	if out.LatestFirmware != nil || out.UpdateAvailable != nil || app.firmware.inFlight || !app.firmware.at.IsZero() {
		t.Errorf("disabled check: latest=%v update=%v attempted=%v", out.LatestFirmware, out.UpdateAvailable, !app.firmware.at.IsZero())
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

func TestWeatherStateFacesAreTheKnobViews(t *testing.T) {
	app := goldenApp(t)
	cfg := app.cfg.Load().Weather
	f := app.buildWeatherState(goldenNow).Faces
	view := newKnobWeatherDetail(app.weather.state(cfg, goldenNow), cfg)
	if f == nil || view.Now == nil || view.Rain == nil || view.Twilight == nil || view.Today == nil {
		t.Fatalf("faces = %+v, view = %+v, want both with wind, rain, twilight and today", f, view)
	}
	eq := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	if f.UTCOffsetS != view.UTCOffset || !eq(f.WindKmh, view.Now.WindKmh) ||
		!eq(f.WindDirDeg, view.Now.WindDir) || !eq(f.RainIntensity, view.Now.Rain) || !eq(f.TodayPopPct, view.Today.Pop) ||
		!eq(f.AQI, view.AQI) {
		t.Errorf("faces now/today/aqi = %+v, knob view now %+v today %+v aqi %v", f, view.Now, view.Today, view.AQI)
	}
	if f.Rain == nil || len(f.Rain.Spells) != len(view.Rain.Spells) || f.Rain.Until == nil || f.Rain.Until.Unix() != *view.Rain.Until {
		t.Fatalf("rain = %+v, knob view rain %+v", f.Rain, view.Rain)
	}
	for i, s := range f.Rain.Spells {
		v := view.Rain.Spells[i]
		if s.Start.Unix() != v[0] || s.End.Unix() != v[1] || s.Intensity != v[2] || s.Snow != (v[3] == 1) {
			t.Errorf("spell %d = %+v, knob view %v", i, s, v)
		}
	}
	if len(f.Days) != len(view.Days) || len(f.Days) != 2 {
		t.Fatalf("days = %+v, knob view %+v, want the 2 days after today", f.Days, view.Days)
	}
	for i, d := range f.Days {
		v := view.Days[i]
		if d.Start.Unix() != v.T || d.End.Unix() != v.End || !eq(d.MinC, v.MinC) || !eq(d.MaxC, v.MaxC) || !eq(d.PopPct, v.Pop) ||
			d.Condition == nil || *d.Condition != v.Cond || d.ConditionCode == nil || *d.ConditionCode != v.Code {
			t.Errorf("day %d = %+v, knob view %+v", i, d, v)
		}
	}
	tw, vt := f.Twilight, view.Twilight
	for name, p := range map[string][2]any{"dawn": {tw.Dawn, vt.Dawn}, "blue_end": {tw.BlueEnd, vt.BlueEnd},
		"golden_end": {tw.GoldenEnd, vt.GoldenEnd}, "golden_start": {tw.GoldenStart, vt.GoldenStart},
		"blue_start": {tw.BlueStart, vt.BlueStart}, "dusk": {tw.Dusk, vt.Dusk}} {
		got, want := p[0].(*time.Time), p[1].(*int64)
		if got == nil || want == nil || got.Unix() != *want || got.Unix()%int64(sunRounding/time.Second) != 0 {
			t.Errorf("twilight %s = %v, knob view %v, want the same time on the %v grid", name, got, want, sunRounding)
		}
	}
	if f.Moon.PhasePct != view.Moon.Phase || f.Moon.IllumPct != view.Moon.Illum || f.Moon.Up == nil {
		t.Errorf("moon = %+v, knob view %+v", f.Moon, view.Moon)
	}
	if f.Night != app.knobWeather(goldenNow).Night {
		t.Errorf("night = %v, want the knob view's", f.Night)
	}

	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rise", "set", "sunrise", "sunset"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("faces carry %q: %s", k, raw)
		}
	}
}

func TestWeatherStateFacesFollowTheKnobsGates(t *testing.T) {
	app := goldenApp(t)
	app.updateConfig(func(c *Config) { c.Weather.AirTile = new(false) })
	if f := app.buildWeatherState(goldenNow).Faces; f == nil || f.AQI != nil {
		t.Errorf("faces = %+v, want no aqi with the air tile off, as on the knob", f)
	}
	cfg := weather.Config{Enabled: true}
	obs := weather.Observation{Condition: "clear", TempC: 10, FetchedAt: goldenNow}
	f := newWeatherFacesOut(newWeatherState(cfg, obs, true, weather.AirObservation{}, false, goldenNow), cfg, goldenZone)
	if f == nil || f.Rain != nil || f.RainIntensity != nil || f.Twilight != nil || f.Moon.Up != nil {
		t.Errorf("faces = %+v, want no rain without steps and no twilight or moon.up without a location", f)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rain", "twilight", "rain_intensity", "aqi"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, ok)
		}
	}
}

func TestWeatherStateFacesMoonUpIsOnTheSunGrid(t *testing.T) {
	app := goldenApp(t)
	cfg := app.cfg.Load().Weather
	at := func(now time.Time) bool {
		t.Helper()
		f := newWeatherFacesOut(app.weather.state(cfg, now), cfg, goldenZone)
		if f == nil || f.Moon.Up == nil {
			t.Fatalf("faces = %+v, want moon.up with a location", f)
		}
		return *f.Moon.Up
	}
	rise, set, _ := weather.MoonUpWindow(cfg.Latitude, cfg.Longitude, goldenNow)
	checked := 0
	for _, e := range []time.Time{rise, set} {
		if e.IsZero() {
			continue
		}
		for d := time.Second; d < sunRounding/2; d += 7 * time.Second {
			a, b := e.Add(-d), e.Add(d)
			if a.Round(sunRounding) != b.Round(sunRounding) {
				continue
			}
			checked++
			if at(a) != at(b) {
				t.Fatalf("moon.up flips between %v and %v around the crossing %v, inside one %v bucket", a, b, e, sunRounding)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no instant pair inside one bucket around a crossing; pick another fixture time")
	}
}
