package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

type statsFixture struct {
	app *App
	srv *httptest.Server
	clk *stepClock
	m   mintResp
}

func newStatsFixture(t *testing.T) *statsFixture {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	clk := &stepClock{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	app.devices.Now = clk.Now
	app.knobStats.now = clk.Now
	return &statsFixture{app: app, srv: srv, clk: clk, m: mintKnob(t, srv, http.StatusCreated)}
}

func (f *statsFixture) setDiagnostics(t *testing.T, level string) {
	t.Helper()
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"diagnostics":"`+level+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set diagnostics %s = %d: %s", level, resp.StatusCode, b)
	}
}

func (f *statsFixture) checkinStats(t *testing.T, stats string) (*http.Response, map[string]any) {
	t.Helper()
	body := `{"fw":"0.6.0","rssi":-61,"heap_internal_free":47104,"heap_internal_largest":31744,"uptime_s":900,"config_version":1`
	if stats != "" {
		body += `,"stats":` + stats
	}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, body+"}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp, out
}

type statsResp struct {
	DeviceID       string           `json:"device_id"`
	Diagnostics    string           `json:"diagnostics"`
	StatsIntervalS int              `json:"stats_interval_s"`
	LiveIntervalS  int              `json:"live_interval_s"`
	Range          string           `json:"range"`
	Online         bool             `json:"online"`
	LastSeen       *time.Time       `json:"last_seen"`
	LiveUntil      *time.Time       `json:"live_until"`
	ResetReason    *string          `json:"reset_reason"`
	Latest         map[string]any   `json:"latest"`
	Points         []map[string]any `json:"points"`
}

func (f *statsFixture) stats(t *testing.T, rng string) statsResp {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/stats?range="+rng, testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats = %d: %s", resp.StatusCode, b)
	}
	var out statsResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

const basicStats = `{"period_ms":60000,"cpu_pct":[12,40],"heap_internal_min":30000,"psram_free":7000000,"psram_min":6500000,"psram_largest":6000000,"temp_c":41.5,"reset_reason":"poweron"}`

const fullStats = `{"period_ms":60000,"cpu_pct":[12,40],"heap_internal_min":30000,"psram_free":7000000,"psram_min":6500000,"psram_largest":6000000,"temp_c":41.5,"reset_reason":"poweron",` +
	`"req_ok":28,"req_fail":2,"req_ms_avg":35.5,"req_ms_max":120,"fps":29.5,"frame_ms_avg":12.25,"frame_ms_max":40,"inv_areas":5400,"inv_px":3456000}`

func num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%s = %v, want a number (sample %v)", key, m[key], m)
	}
	return v
}

func TestDeviceConfigPutDiagnosticsMergesAndValidates(t *testing.T) {
	f := newStatsFixture(t)
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"diagnostics":"full"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"diagnostics":"full"`) {
		t.Fatalf("put full = %d: %s", resp.StatusCode, b)
	}
	if resp.Header.Get(deviceConfigVersion) != "2" {
		t.Fatalf("version = %s, want 2", resp.Header.Get(deviceConfigVersion))
	}
	if resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"diagnostics":"loud"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put loud = %d, want 400: %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"poll_ms":3000}`); !strings.Contains(string(b), `"diagnostics":"full"`) {
		t.Fatalf("unrelated put dropped diagnostics: %d %s", resp.StatusCode, b)
	}
}

func TestDevicesLoadFillsMissingDiagnostics(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	blob, _, _ := app.store.GetSetting(devices.StateKey)
	legacy := strings.Replace(blob, `,"diagnostics":"off"`, "", 1)
	if legacy == blob {
		t.Fatalf("stored blob lacks diagnostics: %s", blob)
	}
	if err := app.store.PutSetting(devices.StateKey, legacy); err != nil {
		t.Fatal(err)
	}
	if err := app.devices.Load(); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := app.devices.Config(m.ID)
	if err != nil || cfg.Diagnostics != "off" {
		t.Fatalf("diagnostics after legacy load = %q (%v), want off", cfg.Diagnostics, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy config invalid after load: %v", err)
	}
}

func TestKnobStatsCheckinStoresSample(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	f.checkinStats(t, fullStats)
	out := f.stats(t, "15m")
	if out.DeviceID != f.m.ID || out.Diagnostics != "full" || out.Range != "15m" || !out.Online {
		t.Fatalf("header = %+v", out)
	}
	if out.ResetReason == nil || *out.ResetReason != "poweron" {
		t.Fatalf("reset_reason = %v", out.ResetReason)
	}
	if out.LastSeen == nil || !out.LastSeen.Equal(f.clk.Now()) {
		t.Fatalf("last_seen = %v", out.LastSeen)
	}
	if len(out.Points) != 1 || out.Latest == nil {
		t.Fatalf("points = %d latest = %v", len(out.Points), out.Latest)
	}
	s := out.Latest
	cpu, _ := s["cpu_percent"].([]any)
	if len(cpu) != 2 || cpu[0].(float64) != 12 || cpu[1].(float64) != 40 {
		t.Fatalf("cpu_percent = %v", s["cpu_percent"])
	}
	checks := map[string]float64{
		"rssi_dbm": -61, "uptime_sec": 900,
		"heap_internal_free_bytes": 47104, "heap_internal_largest_bytes": 31744, "heap_internal_min_bytes": 30000,
		"psram_free_bytes": 7000000, "psram_min_bytes": 6500000, "psram_largest_bytes": 6000000,
		"temp_c": 41.5, "requests_per_min": 30, "request_failures_per_min": 2,
		"request_latency_avg_ms": 35.5, "request_latency_max_ms": 120,
		"render_fps": 29.5, "frame_avg_ms": 12.25, "frame_max_ms": 40,
		"invalidated_areas_per_sec": 90, "invalidated_px_per_sec": 57600,
	}
	for k, want := range checks {
		if got := num(t, s, k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if _, err := time.Parse(time.RFC3339, s["t"].(string)); err != nil || strings.Contains(s["t"].(string), ".") {
		t.Errorf("t = %v, want whole-second RFC 3339", s["t"])
	}
}

func TestKnobStatsBasicLeavesFullFieldsNull(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	f.checkinStats(t, basicStats)
	s := f.stats(t, "1h").Latest
	for _, k := range []string{"requests_per_min", "request_failures_per_min", "request_latency_avg_ms", "render_fps", "frame_max_ms", "invalidated_areas_per_sec", "invalidated_px_per_sec"} {
		v, present := s[k]
		if !present || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, present)
		}
	}
}

func TestKnobStatsCheckinWithoutStatsStoresNothing(t *testing.T) {
	f := newStatsFixture(t)
	f.checkinStats(t, "")
	out := f.stats(t, "15m")
	if out.Latest != nil || len(out.Points) != 0 {
		t.Fatalf("latest = %v points = %d, want none", out.Latest, len(out.Points))
	}
	if out.Points == nil {
		t.Fatal("points = null, want []")
	}
	if !out.Online || out.LastSeen == nil {
		t.Fatalf("online = %v last_seen = %v, want the checkin", out.Online, out.LastSeen)
	}
}

func TestKnobStatsInvalidStatsAreDroppedButCheckinSucceeds(t *testing.T) {
	cases := map[string]string{
		"cpu above 100":       `{"cpu_pct":[150]}`,
		"too many cores":      `{"cpu_pct":[1,2,3,4,5,6,7,8,9]}`,
		"negative bytes":      `{"psram_free":-1}`,
		"hot chip":            `{"temp_c":500}`,
		"bad reset reason":    `{"reset_reason":"Power On!"}`,
		"negative count":      `{"req_ok":-3}`,
		"negative inv_areas":  `{"inv_areas":-1}`,
		"negative inv_px":     `{"inv_px":-1}`,
		"period out of range": `{"period_ms":-5}`,
		"not an object":       `[1,2]`,
	}
	for name, stats := range cases {
		t.Run(name, func(t *testing.T) {
			f := newStatsFixture(t)
			resp, out := f.checkinStats(t, stats)
			if resp.StatusCode != http.StatusOK || out["config_version"] == nil {
				t.Fatalf("checkin = %d %v", resp.StatusCode, out)
			}
			if got := f.stats(t, "15m"); got.Latest != nil {
				t.Fatalf("invalid stats stored: %v", got.Latest)
			}
		})
	}
}

func TestKnobStatsRequestValidation(t *testing.T) {
	f := newStatsFixture(t)
	cases := []struct {
		path, token string
		want        int
	}{
		{"/v1/devices/" + f.m.ID + "/stats?range=2h", testToken, http.StatusBadRequest},
		{"/v1/devices/" + f.m.ID + "/stats", testToken, http.StatusOK},
		{"/v1/devices/knob-000000/stats?range=1h", testToken, http.StatusNotFound},
		{"/v1/devices/" + f.m.ID + "/stats?range=1h", f.m.Token, http.StatusUnauthorized},
		{"/v1/devices/" + f.m.ID + "/stats?range=1h", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		if resp, b := devReq(t, f.srv, "GET", c.path, c.token, ""); resp.StatusCode != c.want {
			t.Errorf("GET %s = %d, want %d: %s", c.path, resp.StatusCode, c.want, b)
		}
	}
	if out := f.stats(t, "24h"); out.Range != "24h" {
		t.Errorf("range = %q", out.Range)
	}
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/stats", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"range":"1h"`) {
		t.Errorf("default range: %s", b)
	}
}

func liveSample(cpu float64, periodMS int64, reqOK int, fpsMax int) string {
	return `{"period_ms":` + strconv.FormatInt(periodMS, 10) + `,"cpu_pct":[` + strconv.FormatFloat(cpu, 'f', -1, 64) +
		`],"heap_internal_min":` + strconv.Itoa(30000-int(cpu)) + `,"req_ok":` + strconv.Itoa(reqOK) +
		`,"req_fail":0,"frame_ms_max":` + strconv.Itoa(fpsMax) + `,"temp_c":40}`
}

func TestKnobStatsLiveSamplesMergeIntoMinuteBuckets(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	for i := range 12 {
		f.clk.advance(5 * time.Second)
		f.checkinStats(t, liveSample(float64(10*(i%2)), 5000, 1, 10+i))
	}
	live := f.stats(t, "15m")
	if len(live.Points) != 12 {
		t.Fatalf("15m points = %d, want 12 live samples", len(live.Points))
	}
	hour := f.stats(t, "1h")
	if len(hour.Points) != 2 {
		t.Fatalf("1h points = %d, want 2 minute buckets (12:00 and 12:01)", len(hour.Points))
	}
	b := hour.Points[0]
	cpu := b["cpu_percent"].([]any)[0].(float64)
	if math.Abs(cpu-50.0/11) > 0.01 {
		t.Errorf("merged cpu = %v, want the period-weighted mean 50/11", cpu)
	}
	if got := num(t, b, "frame_max_ms"); got != 20 {
		t.Errorf("merged frame_max_ms = %v, want the max 20", got)
	}
	if got := num(t, b, "heap_internal_min_bytes"); got != 29990 {
		t.Errorf("merged heap_internal_min_bytes = %v, want the min 29990", got)
	}
	if got := num(t, b, "requests_per_min"); got != 12 {
		t.Errorf("merged requests_per_min = %v, want 12", got)
	}
	if got := hour.Points[0]["t"].(string); got != "2026-10-04T12:00:55Z" {
		t.Errorf("bucket t = %s, want its newest report", got)
	}
}

func TestKnobStatsFirmwareWithoutInvalidatedAreasLeavesThemNull(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	f.checkinStats(t, strings.Replace(fullStats, `,"inv_areas":5400,"inv_px":3456000`, "", 1))
	s := f.stats(t, "15m").Latest
	if got := num(t, s, "render_fps"); got != 29.5 {
		t.Fatalf("render_fps = %v, want 29.5", got)
	}
	for _, k := range []string{"invalidated_areas_per_sec", "invalidated_px_per_sec"} {
		if v, present := s[k]; !present || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, present)
		}
	}
}

func TestKnobStatsInvalidatedAreasMergeAsPeriodWeightedRates(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	f.clk.advance(5 * time.Second)
	f.checkinStats(t, `{"period_ms":10000,"inv_areas":1000,"inv_px":640000}`)
	f.clk.advance(5 * time.Second)
	f.checkinStats(t, `{"period_ms":30000,"inv_areas":600,"inv_px":96000}`)
	f.clk.advance(5 * time.Second)
	f.checkinStats(t, `{"period_ms":5000}`)
	b := f.stats(t, "1h").Points[0]
	if got := num(t, b, "invalidated_areas_per_sec"); got != 40 {
		t.Errorf("merged invalidated_areas_per_sec = %v, want (1000+600)/40 s = 40", got)
	}
	if got := num(t, b, "invalidated_px_per_sec"); got != 18400 {
		t.Errorf("merged invalidated_px_per_sec = %v, want (640000+96000)/40 s = 18400", got)
	}
}

func TestKnobStatsInvalidatedAreasWeighOnlyMeasuredWindows(t *testing.T) {
	measured := func(ms, areas int) string {
		return `{"period_ms":` + strconv.Itoa(ms) + `,"inv_areas":` + strconv.Itoa(areas) + `,"inv_px":` + strconv.Itoa(areas*64) + `}`
	}
	missing := func(ms int) string { return `{"period_ms":` + strconv.Itoa(ms) + `}` }
	cases := map[string]struct {
		rng     string
		reports []string
		step    time.Duration
		want    float64
	}{
		"missing middle": {"1h", []string{measured(10000, 1000), missing(30000), measured(10000, 200)}, 10 * time.Second, 60},
		"missing first":  {"1h", []string{missing(30000), measured(10000, 1000), measured(10000, 200)}, 10 * time.Second, 60},
		"five-minute bucket of partly measured minutes": {"24h", []string{
			measured(10000, 1000), missing(50000), measured(30000, 600), missing(30000),
		}, 30 * time.Second, 40},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newStatsFixture(t)
			f.setDiagnostics(t, "full")
			for _, r := range c.reports {
				f.clk.advance(c.step)
				f.checkinStats(t, r)
			}
			points := f.stats(t, c.rng).Points
			if len(points) != 1 {
				t.Fatalf("%s points = %d, want 1 bucket", c.rng, len(points))
			}
			if got := num(t, points[0], "invalidated_areas_per_sec"); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("invalidated_areas_per_sec = %v, want %v over the measured windows only", got, c.want)
			}
			if got := num(t, points[0], "invalidated_px_per_sec"); math.Abs(got-64*c.want) > 1e-9 {
				t.Errorf("invalidated_px_per_sec = %v, want %v", got, 64*c.want)
			}
		})
	}
}

func TestKnobStatsRenderAndRequestFieldsWeighOnlyMeasuredWindows(t *testing.T) {
	measured := func(ms, v int) string {
		n := strconv.Itoa(v * ms / 1000)
		return `{"period_ms":` + strconv.Itoa(ms) + `,"fps":` + strconv.Itoa(v) + `,"frame_ms_avg":` + strconv.Itoa(v) +
			`,"req_ok":` + n + `,"req_fail":` + n + `,"req_ms_avg":` + strconv.Itoa(v) + `}`
	}
	missing := func(ms int) string { return `{"period_ms":` + strconv.Itoa(ms) + `}` }
	want := map[string]float64{
		"render_fps":       1,
		"requests_per_min": 120, "request_failures_per_min": 60,
	}
	cases := map[string]struct {
		rng     string
		reports []string
		step    time.Duration
		want    float64
	}{
		"missing middle": {"1h", []string{measured(10000, 100), missing(30000), measured(10000, 20)}, 10 * time.Second, 60},
		"missing first":  {"1h", []string{missing(30000), measured(10000, 100), measured(10000, 20)}, 10 * time.Second, 60},
		"five-minute bucket of partly measured minutes": {"24h", []string{
			measured(10000, 100), missing(50000), measured(30000, 20), missing(30000),
		}, 30 * time.Second, 40},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newStatsFixture(t)
			f.setDiagnostics(t, "full")
			for _, r := range c.reports {
				f.clk.advance(c.step)
				f.checkinStats(t, r)
			}
			points := f.stats(t, c.rng).Points
			if len(points) != 1 {
				t.Fatalf("%s points = %d, want 1 bucket", c.rng, len(points))
			}
			for key, scale := range want {
				if got := num(t, points[0], key); math.Abs(got-scale*c.want) > 1e-9 {
					t.Errorf("%s = %v, want %v over the measured windows only", key, got, scale*c.want)
				}
			}
		})
	}
}

type weighReport struct {
	periodMS        int
	sentPeriod      string
	v, reqs         int
	missing, zeroed string
}

func (r weighReport) json() string {
	parts := []string{}
	if r.sentPeriod != "omit" {
		parts = append(parts, `"period_ms":`+cmp.Or(r.sentPeriod, strconv.Itoa(r.periodMS)))
	}
	for _, k := range []string{"cpu", "fps", "frame", "latency", "requests", "inv"} {
		if k == r.missing {
			continue
		}
		v, reqs, areas := r.v, r.reqs, r.v*r.periodMS/1000
		if k == r.zeroed {
			v, reqs, areas = 0, 0, 0
		}
		parts = append(parts, map[string]string{
			"cpu":      fmt.Sprintf(`"cpu_pct":[%d]`, v),
			"fps":      fmt.Sprintf(`"fps":%d`, v),
			"frame":    fmt.Sprintf(`"frame_ms_avg":%d`, v),
			"latency":  fmt.Sprintf(`"req_ms_avg":%d`, v),
			"requests": fmt.Sprintf(`"req_ok":%d,"req_fail":%d`, reqs, reqs),
			"inv":      fmt.Sprintf(`"inv_areas":%d,"inv_px":%d`, areas, 64*areas),
		}[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestKnobStatsEachFieldWeighsOnlyItsOwnReports(t *testing.T) {
	r1 := weighReport{periodMS: 10000, v: 100, reqs: 10}
	r3 := weighReport{periodMS: 20000, v: 50, reqs: 5}
	mid := func(missing, zeroed string) weighReport {
		return weighReport{periodMS: 30000, v: 20, reqs: 60, missing: missing, zeroed: zeroed}
	}
	full := map[string]float64{
		"cpu": 130.0 / 3, "render_fps": 130.0 / 3, "frame_avg_ms": 162000.0 / 2600, "request_latency_avg_ms": 4900.0 / 150,
		"requests_per_min": 150, "request_failures_per_min": 75, "invalidated_areas_per_sec": 130.0 / 3,
	}
	with := func(over map[string]float64) map[string]float64 {
		out := map[string]float64{}
		for k, v := range full {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	cases := map[string]struct {
		reports []weighReport
		want    map[string]float64
	}{
		"all fields":      {[]weighReport{r1, mid("", ""), r3}, full},
		"cpu missing":     {[]weighReport{r1, mid("cpu", ""), r3}, with(map[string]float64{"cpu": 200.0 / 3})},
		"cpu zero":        {[]weighReport{r1, mid("", "cpu"), r3}, with(map[string]float64{"cpu": 100.0 / 3})},
		"fps missing":     {[]weighReport{r1, mid("fps", ""), r3}, with(map[string]float64{"render_fps": 200.0 / 3, "frame_avg_ms": 75})},
		"fps zero":        {[]weighReport{r1, mid("", "fps"), r3}, with(map[string]float64{"render_fps": 100.0 / 3, "frame_avg_ms": 75})},
		"frame missing":   {[]weighReport{r1, mid("frame", ""), r3}, with(map[string]float64{"frame_avg_ms": 75})},
		"frame zero":      {[]weighReport{r1, mid("", "frame"), r3}, with(map[string]float64{"frame_avg_ms": 150000.0 / 2600})},
		"latency missing": {[]weighReport{r1, mid("latency", ""), r3}, with(map[string]float64{"request_latency_avg_ms": 250.0 / 3})},
		"latency zero":    {[]weighReport{r1, mid("", "latency"), r3}, with(map[string]float64{"request_latency_avg_ms": 2500.0 / 150})},
		"request counts missing": {[]weighReport{r1, mid("requests", ""), r3}, with(map[string]float64{
			"requests_per_min": 60, "request_failures_per_min": 30, "request_latency_avg_ms": 250.0 / 3})},
		"request counts zero": {[]weighReport{r1, mid("", "requests"), r3}, with(map[string]float64{
			"requests_per_min": 30, "request_failures_per_min": 15, "request_latency_avg_ms": 250.0 / 3})},
		"invalidated missing": {[]weighReport{r1, mid("inv", ""), r3}, with(map[string]float64{"invalidated_areas_per_sec": 200.0 / 3})},
		"invalidated zero":    {[]weighReport{r1, mid("", "inv"), r3}, with(map[string]float64{"invalidated_areas_per_sec": 100.0 / 3})},
		"period omitted or zero": {[]weighReport{
			{periodMS: 60000, sentPeriod: "omit", v: 100, reqs: 10},
			{periodMS: 60000, sentPeriod: "0", v: 20, reqs: 60},
			{periodMS: 60000, sentPeriod: "omit", v: 50, reqs: 5},
		}, map[string]float64{
			"cpu": 170.0 / 3, "render_fps": 170.0 / 3, "frame_avg_ms": 774000.0 / 10200, "request_latency_avg_ms": 4900.0 / 150,
			"requests_per_min": 50, "request_failures_per_min": 25, "invalidated_areas_per_sec": 170.0 / 3,
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newStatsFixture(t)
			f.setDiagnostics(t, "full")
			for _, r := range c.reports {
				f.clk.advance(10 * time.Second)
				f.checkinStats(t, r.json())
			}
			points := f.stats(t, "1h").Points
			if len(points) != 1 {
				t.Fatalf("1h points = %d, want 1 bucket", len(points))
			}
			p := points[0]
			for key, want := range c.want {
				var got float64
				if key == "cpu" {
					got = p["cpu_percent"].([]any)[0].(float64)
				} else {
					got = num(t, p, key)
				}
				if math.Abs(got-want) > 1e-9 {
					t.Errorf("%s = %v, want %v", key, got, want)
				}
			}
			if got, want := num(t, p, "invalidated_px_per_sec"), 64*c.want["invalidated_areas_per_sec"]; math.Abs(got-want) > 1e-6 {
				t.Errorf("invalidated_px_per_sec = %v, want %v", got, want)
			}
		})
	}
}

func TestKnobStatsFrameTimeIsAMeanPerFrame(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	for _, r := range []string{
		`{"period_ms":10000,"fps":60,"frame_ms_avg":10}`,
		`{"period_ms":50000,"fps":2,"frame_ms_avg":40}`,
		`{"period_ms":10000,"frame_ms_avg":99}`,
	} {
		f.clk.advance(10 * time.Second)
		f.checkinStats(t, r)
	}
	b := f.stats(t, "1h").Points[0]
	if got, want := num(t, b, "frame_avg_ms"), (600*10.0+100*40.0)/700; math.Abs(got-want) > 1e-9 {
		t.Errorf("frame_avg_ms = %v, want %v: weighted by the frames each report rendered", got, want)
	}
}

func TestKnobStatsIgnoreAStaleCheckin(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	send := func(uptime int, cpu int) {
		t.Helper()
		body := `{"fw":"0.9.70","uptime_s":` + strconv.Itoa(uptime) + `,"diag":{"boots":40},"stats":{"period_ms":10000,"cpu_pct":[` + strconv.Itoa(cpu) + `]}}`
		resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
		}
	}
	send(900, 10)
	f.clk.advance(time.Second)
	send(880, 90)
	latest := f.stats(t, "15m").Latest
	if got := num(t, latest, "uptime_sec"); got != 900 {
		t.Errorf("latest uptime_sec = %v, want 900 from the newer report", got)
	}
	if got := latest["cpu_percent"].([]any)[0].(float64); got != 10 {
		t.Errorf("latest cpu = %v, want 10: a stale checkin was recorded as the newest sample", got)
	}
}

func TestKnobStats15mUsesMinuteSamplesBeforeLiveWindow(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	for range 13 {
		f.clk.advance(time.Minute)
		f.checkinStats(t, basicStats)
	}
	out := f.stats(t, "15m")
	if len(out.Points) != 13 {
		t.Fatalf("15m points = %d, want 13", len(out.Points))
	}
	for i := 1; i < len(out.Points); i++ {
		if out.Points[i]["t"].(string) <= out.Points[i-1]["t"].(string) {
			t.Fatalf("points not ascending at %d: %v", i, out.Points)
		}
	}
}

func TestKnobStats24hDownsamplesToFiveMinutes(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	for range 120 {
		f.clk.advance(time.Minute)
		f.checkinStats(t, basicStats)
	}
	out := f.stats(t, "24h")
	if len(out.Points) < 24 || len(out.Points) > 25 {
		t.Fatalf("24h points = %d, want 24-25 five-minute buckets", len(out.Points))
	}
	if n := len(f.stats(t, "1h").Points); n < 60 || n > 61 {
		t.Fatalf("1h points = %d, want 60-61", n)
	}
}

func TestKnobStatsMinuteRingKeepsOnly24Hours(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	start := f.clk.Now()
	for range 25 * 60 {
		f.clk.advance(time.Minute)
		f.app.knobStats.record(f.m.ID, f.clk.Now(), knobSampleFromReport(devices.Checkin{RSSI: -60}, &knobStatsReport{PeriodMS: 60000}))
	}
	out := f.stats(t, "24h")
	first, _ := time.Parse(time.RFC3339, out.Points[0]["t"].(string))
	if first.Before(f.clk.Now().Add(-24*time.Hour - 5*time.Minute)) {
		t.Fatalf("oldest point %v older than 24 h (start %v)", first, start)
	}
	if n := f.app.knobStats.minuteLen(f.m.ID); n > statsMinuteCap {
		t.Fatalf("minute ring holds %d, cap %d", n, statsMinuteCap)
	}
}

func TestKnobStatsLiveRequiresDiagnostics(t *testing.T) {
	f := newStatsFixture(t)
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("live with diagnostics off = %d, want 409: %s", resp.StatusCode, b)
	}
	if resp, _ := devReq(t, f.srv, "POST", "/v1/devices/knob-000000/stats/live", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("live on unknown device = %d, want 404", resp.StatusCode)
	}
	if resp, _ := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", f.m.Token, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("live with device token = %d, want 401", resp.StatusCode)
	}
}

func TestKnobStatsLiveBodyValidation(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	for _, body := range []string{`{"seconds":601}`, `{"seconds":-1}`, `{"secs":5}`} {
		if resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("live %s = %d, want 400: %s", body, resp.StatusCode, b)
		}
	}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"live_until":"2026-10-04T12:03:00Z"`) {
		t.Fatalf("default live = %d %s, want 180 s", resp.StatusCode, b)
	}
}

func TestKnobStatsLiveDeliveredInCheckinAndView(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	until := f.clk.Now().Add(120 * time.Second).Unix()
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":120}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"live_until":"2026-10-04T12:02:00Z"`) {
		t.Fatalf("live = %d %s", resp.StatusCode, b)
	}
	if got := f.stats(t, "15m").LiveUntil; got == nil || got.Unix() != until {
		t.Fatalf("stats live_until = %v", got)
	}
	resp, out := f.checkinStats(t, basicStats)
	if got, _ := out["diag_live_until"].(float64); int64(got) != until {
		t.Fatalf("checkin diag_live_until = %v, want %d", out["diag_live_until"], until)
	}
	if resp.Header.Get(knobNowHeader) == "" {
		t.Fatal("checkin lacks X-Ember-Now")
	}
	body, _, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil || !strings.HasSuffix(string(body), `,"diag_live_until":`+strconv.FormatInt(until, 10)+`}`) {
		t.Fatalf("view = %s (%v)", body, err)
	}

	devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":0}`)
	if body, _, _ := f.app.knobView(f.m.ID, f.clk.Now()); strings.Contains(string(body), "diag_live_until") {
		t.Fatalf("view after stop = %s", body)
	}
	if _, out := f.checkinStats(t, ""); out["diag_live_until"] != nil {
		t.Fatalf("checkin after stop carries %v", out["diag_live_until"])
	}
}

func TestKnobStatsLiveExpires(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":60}`)
	f.clk.advance(61 * time.Second)
	if body, _, _ := f.app.knobView(f.m.ID, f.clk.Now()); strings.Contains(string(body), "diag_live_until") {
		t.Fatalf("view after expiry = %s", body)
	}
	if got := f.stats(t, "15m").LiveUntil; got != nil {
		t.Fatalf("stats live_until after expiry = %v", got)
	}
}

func TestKnobStatsLiveDroppedWhenDiagnosticsTurnOff(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":300}`)
	f.setDiagnostics(t, "off")
	if body, _, _ := f.app.knobView(f.m.ID, f.clk.Now()); strings.Contains(string(body), "diag_live_until") {
		t.Fatalf("view with diagnostics off = %s", body)
	}
}

func TestKnobViewWithoutLiveModeIsUnchanged(t *testing.T) {
	f := newStatsFixture(t)
	body, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	if strings.Contains(string(body), "diag") {
		t.Fatalf("view without live mode = %s", body)
	}
}

func TestKnobStatsForgottenOnDelete(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	f.checkinStats(t, basicStats)
	devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
	if n := f.app.knobStats.minuteLen(f.m.ID); n != 0 {
		t.Fatalf("minute ring after delete holds %d", n)
	}
}

func TestKnobStatsCheckinDoesNotWriteStore(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, clk := countDeviceWrites(t, app)
	app.knobStats.now = clk.Now
	m := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "PUT", "/v1/devices/"+m.ID+"/config", testToken, `{"diagnostics":"full"}`)
	base := kv.puts.Load()
	for range 20 {
		clk.advance(5 * time.Second)
		devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, `{"config_version":2,"stats":`+fullStats+`}`)
	}
	if got := kv.puts.Load() - base; got != 0 {
		t.Fatalf("20 stats checkins wrote the store %d times, want 0", got)
	}
}

func TestKnobStatsGolden(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "full")
	devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":300}`)
	f.clk.advance(55 * time.Second)
	f.checkinStats(t, fullStats)
	f.clk.advance(5 * time.Second)
	f.checkinStats(t, `{"period_ms":5000,"cpu_pct":[20,55.5],"heap_internal_min":29000,"temp_c":42,"reset_reason":"poweron","req_ok":3,"req_fail":0,"req_ms_avg":30,"req_ms_max":64,"fps":30,"frame_ms_avg":11.5,"frame_ms_max":22,"inv_areas":500,"inv_px":320000}`)
	v, err := f.app.buildKnobStats(f.m.ID, "15m", f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "knob_stats", v)
	empty := newStatsFixture(t)
	v, err = empty.app.buildKnobStats(empty.m.ID, "1h", empty.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "knob_stats_empty", v)
}

func TestKnobStatsIgnoredWhileDiagnosticsOff(t *testing.T) {
	f := newStatsFixture(t)
	f.checkinStats(t, fullStats)
	if got := f.stats(t, "15m"); got.Latest != nil || len(got.Points) != 0 {
		t.Fatalf("stats stored with diagnostics off: %v", got.Latest)
	}
}

func TestKnobStatsBasicDropsFullOnlyFields(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	f.checkinStats(t, fullStats)
	s := f.stats(t, "15m").Latest
	if s == nil || s["temp_c"] == nil || s["cpu_percent"] == nil {
		t.Fatalf("basic fields missing: %v", s)
	}
	for _, k := range []string{"requests_per_min", "request_failures_per_min", "request_latency_avg_ms", "request_latency_max_ms", "render_fps", "frame_avg_ms", "frame_max_ms", "invalidated_areas_per_sec", "invalidated_px_per_sec"} {
		if s[k] != nil {
			t.Errorf("%s = %v at basic, want null", k, s[k])
		}
	}
}

func TestKnobStatsRecordEmberBrightness(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	f.checkinStats(t, basicStats)
	want := f.app.currentBrightness(f.clk.Now()).Level
	if got := num(t, f.stats(t, "15m").Latest, "brightness_level"); got != float64(want) {
		t.Fatalf("brightness_level = %v, want Ember's level %d at the checkin", got, want)
	}
}

func TestDeviceConfigPutIntervalsMergeAndValidate(t *testing.T) {
	f := newStatsFixture(t)
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"stats_interval_s":120,"live_interval_s":2}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"stats_interval_s":120`) || !strings.Contains(string(b), `"live_interval_s":2`) {
		t.Fatalf("put = %d: %s", resp.StatusCode, b)
	}
	for _, body := range []string{`{"stats_interval_s":45}`, `{"stats_interval_s":0}`, `{"live_interval_s":3}`, `{"live_interval_s":"5"}`} {
		if resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("put %s = %d, want 400: %s", body, resp.StatusCode, b)
		}
	}
	resp, b = devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"poll_ms":3000}`)
	if !strings.Contains(string(b), `"stats_interval_s":120`) || !strings.Contains(string(b), `"live_interval_s":2`) {
		t.Fatalf("unrelated put dropped the intervals: %d %s", resp.StatusCode, b)
	}
}

func TestDevicesLoadFillsMissingIntervals(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	m := mintKnob(t, srv, http.StatusCreated)
	blob, _, _ := app.store.GetSetting(devices.StateKey)
	legacy := regexp.MustCompile(`,"(stats|live)_interval_s":\d+`).ReplaceAllString(blob, "")
	if legacy == blob {
		t.Fatalf("stored blob lacks the intervals: %s", blob)
	}
	if err := app.store.PutSetting(devices.StateKey, legacy); err != nil {
		t.Fatal(err)
	}
	if err := app.devices.Load(); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := app.devices.Config(m.ID)
	if err != nil || cfg.StatsIntervalS != devices.KnobStatsIntervalDefault || cfg.LiveIntervalS != devices.KnobLiveIntervalDefault {
		t.Fatalf("intervals after legacy load = %d/%d (%v)", cfg.StatsIntervalS, cfg.LiveIntervalS, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy config invalid after load: %v", err)
	}
}

func TestKnobCheckinDeliversIntervals(t *testing.T) {
	f := newStatsFixture(t)
	devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"stats_interval_s":300,"live_interval_s":10}`)
	_, out := f.checkinStats(t, "")
	cfg, _ := out["config"].(map[string]any)
	if cfg["stats_interval_s"] != float64(300) || cfg["live_interval_s"] != float64(10) {
		t.Fatalf("checkin config = %v, want the intervals", out)
	}
	resp, b := devReq(t, f.srv, "GET", "/v1/devices/self/view", f.m.Token, "")
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	if resp.StatusCode != http.StatusOK || v["config_version"] != float64(2) {
		t.Fatalf("view = %d %s, want config_version 2 so the knob fetches the change", resp.StatusCode, b)
	}
	st := f.stats(t, "1h")
	if st.StatsIntervalS != 300 || st.LiveIntervalS != 10 {
		t.Fatalf("stats view intervals = %d/%d, want 300/10", st.StatsIntervalS, st.LiveIntervalS)
	}
}

func TestKnobStatsLiveRingHoldsTenMinutesAtTwoSeconds(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	for range 330 {
		f.clk.advance(2 * time.Second)
		f.app.knobStats.record(f.m.ID, f.clk.Now(), knobSampleFromReport(devices.Checkin{RSSI: -60}, &knobStatsReport{PeriodMS: 2000}))
	}
	pts := f.stats(t, "15m").Points
	var live int
	cut := f.clk.Now().Add(-statsLiveWindow)
	for _, p := range pts {
		if ts, _ := time.Parse(time.RFC3339, p["t"].(string)); !ts.Before(cut) {
			live++
		}
	}
	if live < 300 {
		t.Fatalf("15m has %d samples in the last 10 min, want 300 (2 s live interval)", live)
	}
}

func TestKnobStatsMinuteRingDropsOlderThanADay(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	for range 2 * 24 * 12 {
		f.clk.advance(5 * time.Minute)
		f.app.knobStats.record(f.m.ID, f.clk.Now(), knobSampleFromReport(devices.Checkin{RSSI: -60}, &knobStatsReport{PeriodMS: 300000}))
	}
	if n := f.app.knobStats.minuteLen(f.m.ID); n > 24*12+1 {
		t.Fatalf("minute ring holds %d buckets at a 5 min interval, want <= %d (24 h)", n, 24*12+1)
	}
	if n := len(f.stats(t, "24h").Points); n < 24*12-1 {
		t.Fatalf("24h points = %d, want a day of 5 min buckets", n)
	}
}

func TestSampleRingKeepsOrderWhenGrowingAfterDrops(t *testing.T) {
	r := newSampleRing[knobSample](8)
	t0 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	at := func(i int) knobSample { return knobSample{T: t0.Add(time.Duration(i) * time.Minute)} }
	for i := range 4 {
		r.push(at(i))
	}
	r.dropBefore(at(2).T)
	for i := 4; i < 11; i++ {
		r.push(at(i))
	}
	got := r.since(time.Time{})
	if len(got) != 8 {
		t.Fatalf("len = %d, want 8", len(got))
	}
	for i, s := range got {
		if want := at(i + 3).T; !s.T.Equal(want) {
			t.Fatalf("[%d] = %v, want %v (order lost)", i, s.T, want)
		}
	}
}

func TestSampleRingGivesBackItsBufferAfterALiveSession(t *testing.T) {
	r := newSampleRing[knobSample](300)
	t0 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for i := range 300 {
		r.push(knobSample{T: t0.Add(time.Duration(i) * 2 * time.Second)})
	}
	if cap(r.buf) < 300 {
		t.Fatalf("cap = %d, want the full live window", cap(r.buf))
	}
	end := t0.Add(600 * time.Second)
	r.push(knobSample{T: end.Add(5 * time.Minute)})
	r.dropBefore(end.Add(time.Second))
	if r.len != 1 || cap(r.buf) > 32 {
		t.Fatalf("len %d cap %d after the session aged out, want 1 sample in a small buffer", r.len, cap(r.buf))
	}
	if got := r.since(time.Time{}); len(got) != 1 || !got[0].T.Equal(end.Add(5*time.Minute)) {
		t.Fatalf("kept %v, want the newest sample", got)
	}
}
