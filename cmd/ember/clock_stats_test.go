package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type clockStatsFixture struct {
	app   *App
	srv   *httptest.Server
	clock *httptest.Server
	hits  atomic.Int32
	t0    time.Time
}

func newClockStatsFixture(t *testing.T) *clockStatsFixture {
	t.Helper()
	f := &clockStatsFixture{t0: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	f.app = newPomodoroApp(t)
	f.clock = ngHealthClock(t, &f.hits)
	f.app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = f.clock.URL })
	f.app.clockStats.now = func() time.Time { return f.t0 }
	f.srv = httptest.NewServer(f.app.routes())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *clockStatsFixture) probe(at time.Duration) {
	f.app.probeClockHealth(context.Background(), f.t0.Add(at))
}

type clockStatsResp struct {
	Range             string           `json:"range"`
	Configured        bool             `json:"configured"`
	Reachable         *bool            `json:"reachable"`
	CheckedAt         *time.Time       `json:"checked_at"`
	IPAddress         *string          `json:"ip_address"`
	SampleIntervalSec int              `json:"sample_interval_sec"`
	Latest            map[string]any   `json:"latest"`
	Points            []map[string]any `json:"points"`
}

func (f *clockStatsFixture) stats(t *testing.T, rng string, now time.Duration) clockStatsResp {
	t.Helper()
	f.app.clockStats.now = func() time.Time { return f.t0.Add(now) }
	resp, b := devReq(t, f.srv, "GET", "/v1/clock/stats?range="+rng, testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clock stats = %d: %s", resp.StatusCode, b)
	}
	var out clockStatsResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

func TestClockStatsRecordsEachProbe(t *testing.T) {
	f := newClockStatsFixture(t)
	f.probe(0)
	f.probe(30 * time.Second)
	got := f.stats(t, "15m", 40*time.Second)
	if len(got.Points) != 2 {
		t.Fatalf("15m points = %d, want 2 (one per probe): %v", len(got.Points), got.Points)
	}
	s := got.Latest
	if s == nil {
		t.Fatal("latest = nil, want the newest probe")
	}
	for key, want := range map[string]float64{
		"rssi_dbm": -71, "free_heap_bytes": 103032, "min_free_heap_bytes": 76544,
		"temperature_c": 33.4, "humidity_percent": 21.4, "battery_percent": 97,
	} {
		if v := num(t, s, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	if s["reachable"] != true {
		t.Errorf("reachable = %v, want true", s["reachable"])
	}
	if got.Reachable == nil || !*got.Reachable || !got.Configured {
		t.Errorf("view reachable/configured = %v/%v, want true/true", got.Reachable, got.Configured)
	}
	if got.IPAddress == nil || *got.IPAddress != "192.0.2.66" {
		t.Errorf("ip_address = %v, want the clock's", got.IPAddress)
	}
	if got.SampleIntervalSec != 30 {
		t.Errorf("sample_interval_sec = %d, want 30", got.SampleIntervalSec)
	}
}

func TestClockStatsCachedProbeRecordsNothing(t *testing.T) {
	f := newClockStatsFixture(t)
	f.probe(0)
	f.probe(10 * time.Second)
	if got := f.stats(t, "15m", 20*time.Second); len(got.Points) != 1 {
		t.Fatalf("points = %d after a cached read, want 1", len(got.Points))
	}
}

func TestClockStatsRequiresOwnerToken(t *testing.T) {
	f := newClockStatsFixture(t)
	if resp, b := devReq(t, f.srv, "GET", "/v1/clock/stats", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401: %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, f.srv, "GET", "/v1/clock/stats?range=2d", testToken, ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("range=2d = %d, want 400: %s", resp.StatusCode, b)
	}
	resp, b := devReq(t, f.srv, "GET", "/v1/clock/stats", testToken, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"range":"1h"`) {
		t.Fatalf("default range = %d: %s", resp.StatusCode, b)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestClockStatsMinuteBucketsAndLiveSamples(t *testing.T) {
	f := newClockStatsFixture(t)
	for i := range 6 {
		f.probe(time.Duration(i) * 30 * time.Second)
	}
	if got := f.stats(t, "1h", 3*time.Minute); len(got.Points) != 3 {
		t.Fatalf("1h points = %d, want 3 one-minute buckets", len(got.Points))
	}
	if got := f.stats(t, "15m", 3*time.Minute); len(got.Points) != 6 {
		t.Fatalf("15m points = %d, want 6 thirty-second samples", len(got.Points))
	}
}

func TestClockStats15mUsesMinutesBeforeTheLiveWindow(t *testing.T) {
	f := newClockStatsFixture(t)
	for i := range 28 {
		f.probe(time.Duration(i) * 30 * time.Second)
	}
	got := f.stats(t, "15m", 14*time.Minute)
	if n := len(got.Points); n != 4+20 {
		t.Fatalf("15m points = %d, want 4 minute buckets + 20 live samples", n)
	}
	for i := 1; i < len(got.Points); i++ {
		a, _ := time.Parse(time.RFC3339, got.Points[i-1]["t"].(string))
		b, _ := time.Parse(time.RFC3339, got.Points[i]["t"].(string))
		if !b.After(a) {
			t.Fatalf("points out of order at %d: %v then %v", i, a, b)
		}
	}
}

func TestClockStats24hDownsamplesToFiveMinutes(t *testing.T) {
	f := newClockStatsFixture(t)
	for i := range 40 {
		f.probe(time.Duration(i) * 30 * time.Second)
	}
	if got := f.stats(t, "24h", 20*time.Minute); len(got.Points) != 4 {
		t.Fatalf("24h points = %d, want 4 five-minute buckets", len(got.Points))
	}
}

func TestClockStatsMinuteRingKeepsOnly24Hours(t *testing.T) {
	f := newClockStatsFixture(t)
	for i := range 25 * 60 {
		f.app.clockStats.record(f.t0.Add(time.Duration(i)*time.Minute), f.clock.URL, clockSample{Reachable: true})
	}
	if n := f.app.clockStats.minuteLen(); n != statsMinuteCap {
		t.Fatalf("minute ring = %d, want %d", n, statsMinuteCap)
	}
}

func TestClockStatsCountsPublishesBetweenProbes(t *testing.T) {
	f := newClockStatsFixture(t)
	f.app.metrics.incPublishOK()
	f.probe(0)
	for range 3 {
		f.app.metrics.incPublishOK()
	}
	f.app.metrics.incPublishFail()
	f.probe(30 * time.Second)
	f.probe(60 * time.Second)
	live := f.stats(t, "15m", 70*time.Second).Points
	if len(live) != 3 {
		t.Fatalf("points = %d, want 3", len(live))
	}
	for i, want := range [][2]float64{{1, 0}, {3, 1}, {0, 0}} {
		if ok, fail := num(t, live[i], "publish_ok"), num(t, live[i], "publish_fail"); ok != want[0] || fail != want[1] {
			t.Errorf("point %d publish ok/fail = %v/%v, want %v/%v", i, ok, fail, want[0], want[1])
		}
	}
	minutes := f.stats(t, "1h", 70*time.Second).Points
	if len(minutes) != 2 || num(t, minutes[0], "publish_ok") != 4 || num(t, minutes[0], "publish_fail") != 1 {
		t.Fatalf("minute buckets = %v, want the first summing 4 ok / 1 fail", minutes)
	}
}

func TestClockStatsUnreachableKeepsTheLastReading(t *testing.T) {
	f := newClockStatsFixture(t)
	f.probe(0)
	f.clock.Close()
	f.probe(30 * time.Second)
	got := f.stats(t, "15m", 40*time.Second)
	if got.Reachable == nil || *got.Reachable {
		t.Fatalf("reachable = %v, want false after a failed probe", got.Reachable)
	}
	if got.Latest == nil || num(t, got.Latest, "rssi_dbm") != -71 {
		t.Fatalf("latest = %v, want the last reading that reached the clock", got.Latest)
	}
	if len(got.Points) != 2 || got.Points[1]["reachable"] != false || got.Points[1]["rssi_dbm"] != nil {
		t.Fatalf("points = %v, want the failed probe as an empty unreachable sample", got.Points)
	}
}

func TestClockStatsWithoutClockIsEmpty(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = "" })
	v := app.buildClockStats("1h", time.Now())
	if v.Configured || v.Reachable != nil || v.Latest != nil || v.Points == nil || len(v.Points) != 0 {
		t.Fatalf("no clock = %+v, want unconfigured, empty points", v)
	}
}

func TestClockStatsMergeKeepsLowWaterAndNewestGauges(t *testing.T) {
	a := clockSample{Reachable: true, RSSIDBm: refOf(-60), FreeHeapBytes: refOf(int64(90_000)), MinFreeHeapBytes: refOf(int64(70_000)),
		TemperatureC: refOf(30.0), PublishOK: 2, PublishFail: 1}
	b := clockSample{Reachable: false, PublishOK: 1}
	m := a.merge(b)
	if !m.Reachable || m.RSSIDBm == nil || *m.RSSIDBm != -60 || *m.TemperatureC != 30 {
		t.Fatalf("merge with an unreachable probe = %+v, want a's readings kept", m)
	}
	if m.PublishOK != 3 || m.PublishFail != 1 {
		t.Fatalf("publish = %d/%d, want sums 3/1", m.PublishOK, m.PublishFail)
	}
	c := clockSample{Reachable: true, RSSIDBm: refOf(-70), MinFreeHeapBytes: refOf(int64(80_000)), FreeHeapBytes: refOf(int64(85_000))}
	m = a.merge(c)
	if *m.RSSIDBm != -70 || *m.FreeHeapBytes != 85_000 || *m.MinFreeHeapBytes != 70_000 {
		t.Fatalf("merge = rssi %d heap %d min %d, want newest gauges and the lowest low-water mark",
			*m.RSSIDBm, *m.FreeHeapBytes, *m.MinFreeHeapBytes)
	}
}

func TestClockStatsSamplerProbesAtStart(t *testing.T) {
	f := newClockStatsFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.app.StartClockSampler(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	recvWithin(t, done, "clock sampler exit")
	if f.hits.Load() == 0 {
		t.Fatal("sampler never probed the clock")
	}
}

func TestClockStatsGolden(t *testing.T) {
	f := newClockStatsFixture(t)
	f.app.metrics.incPublishOK()
	f.probe(0)
	f.app.metrics.incPublishFail()
	f.probe(30 * time.Second)
	assertGolden(t, "clock_stats", f.app.buildClockStats("15m", f.t0.Add(45*time.Second)))
	empty := newPomodoroApp(t)
	empty.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = closedURL(t) })
	assertGolden(t, "clock_stats_empty", empty.buildClockStats("1h", f.t0))
}

func TestDeviceWatchProbeSharesTheHealthProbe(t *testing.T) {
	f := newClockStatsFixture(t)
	now := time.Now()
	f.app.probeClockHealth(context.Background(), now)
	p := f.app.probeDevice(context.Background(), 15*time.Second)
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("clock GETs = %d after a health probe and a watch probe within maxAge, want 1", n)
	}
	if !p.reachable || p.uptimeSec != 268719 || !p.at.Equal(now) {
		t.Fatalf("watch probe = %+v, want the health probe's uptime and time", p)
	}
	f.app.probeDevice(context.Background(), 0)
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("clock GETs = %d after a maxAge-0 watch probe, want 2", n)
	}
	if got := f.app.buildClockStats("15m", time.Now()); len(got.Points) != 2 {
		t.Fatalf("points = %d, want the watch probe recorded as a sample too", len(got.Points))
	}
}

func TestClockStatsResetWhenTheClockChanges(t *testing.T) {
	f := newClockStatsFixture(t)
	f.probe(0)
	var otherHits atomic.Int32
	other := ngHealthClock(t, &otherHits)
	f.app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = other.URL })
	got := f.app.buildClockStats("15m", f.t0.Add(10*time.Second))
	if len(got.Points) != 0 || got.Latest != nil || got.IPAddress != nil || got.Reachable != nil {
		t.Fatalf("after a clock change = %+v, want the old clock's data gone", got)
	}
	f.probe(30 * time.Second)
	if got := f.app.buildClockStats("15m", f.t0.Add(40*time.Second)); len(got.Points) != 1 || got.Latest == nil {
		t.Fatalf("points = %d, want only the new clock's sample", len(got.Points))
	}
}
