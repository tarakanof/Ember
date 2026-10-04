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

func TestBrightnessDefaults(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	if c.Floor != 10 || c.Ceiling != 255 || c.NightLevel != 20 || c.DayLevel != 255 {
		t.Errorf("defaults = %+v", c)
	}
	if err := c.validate(); err != nil {
		t.Errorf("defaults invalid: %v", err)
	}
}

func TestBrightnessValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*BrightnessConfig)
		ok   bool
	}{
		{"defaults", func(*BrightnessConfig) {}, true},
		{"floor above ceiling", func(c *BrightnessConfig) { c.Floor, c.Ceiling = 200, 100 }, false},
		{"floor zero", func(c *BrightnessConfig) { c.Floor = 0 }, false},
		{"ceiling over 255", func(c *BrightnessConfig) { c.Ceiling = 256 }, false},
		{"night under floor", func(c *BrightnessConfig) { c.NightLevel = 5 }, false},
		{"day over ceiling", func(c *BrightnessConfig) { c.Ceiling, c.DayLevel = 200, 220 }, false},
		{"lux inverted", func(c *BrightnessConfig) { c.LuxDark, c.LuxBright = 100, 50 }, false},
		{"alpha zero", func(c *BrightnessConfig) { c.EMAAlpha = 0 }, false},
		{"alpha over 1", func(c *BrightnessConfig) { c.EMAAlpha = 1.5 }, false},
		{"hysteresis negative", func(c *BrightnessConfig) { c.Hysteresis = -1 }, false},
		{"stale under two probes", func(c *BrightnessConfig) { c.StaleSeconds = 59 }, false},
		{"stale at two probes", func(c *BrightnessConfig) { c.StaleSeconds = 60 }, true},
		{"hysteresis zero", func(c *BrightnessConfig) { c.Hysteresis = 0 }, false},
		{"twilight zero", func(c *BrightnessConfig) { c.TwilightMinutes = 0 }, false},
		{"twilight too long", func(c *BrightnessConfig) { c.TwilightMinutes = 500 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := BrightnessConfig{}.resolved()
			tc.mut(&c)
			if err := c.validate(); (err == nil) != tc.ok {
				t.Errorf("validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestLuxToLevel(t *testing.T) {
	c := BrightnessConfig{}.resolved() // dark 1 lux, bright 200 lux, 10..255
	cases := []struct {
		lux  float64
		want int
	}{
		{-3, 10}, {0, 10}, {1, 10}, {200, 255}, {5000, 255},
		{14, 132}, // geometric midpoint of 1 and 200 is ~14.1 lux: halfway up
	}
	for _, tc := range cases {
		if got := luxToLevel(c, tc.lux); got != tc.want {
			t.Errorf("luxToLevel(%v) = %d, want %d", tc.lux, got, tc.want)
		}
	}
	prev := 0
	for lux := 0.0; lux < 400; lux += 3 {
		got := luxToLevel(c, lux)
		if got < prev {
			t.Fatalf("not monotone at %v lux: %d after %d", lux, got, prev)
		}
		prev = got
	}
}

func TestHoldWithinBand(t *testing.T) {
	cases := []struct {
		name         string
		prev         int
		hasPrev      bool
		target, band int
		floor, ceil  int
		want         int
	}{
		{"no previous takes target", 0, false, 100, 8, 10, 255, 100},
		{"inside band holds", 100, true, 105, 8, 10, 255, 100},
		{"inside band below holds", 100, true, 93, 8, 10, 255, 100},
		{"at band edge moves", 100, true, 108, 8, 10, 255, 108},
		{"outside band moves", 100, true, 160, 8, 10, 255, 160},
		{"snaps to ceiling inside band", 250, true, 255, 8, 10, 255, 255},
		{"snaps to floor inside band", 14, true, 10, 8, 10, 255, 10},
		{"band of one follows", 100, true, 101, 1, 10, 255, 101},
		{"held level clamps up to a raised floor", 12, true, 16, 8, 15, 255, 15},
		{"held level clamps down to a lowered ceiling", 250, true, 200, 80, 10, 220, 220},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := holdWithinBand(tc.prev, tc.hasPrev, tc.target, tc.band, tc.floor, tc.ceil); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// London midsummer: sun is up from ~03:43Z to ~20:21Z.
var (
	lonLat, lonLon = 51.5, -0.12
	jun21          = time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
)

func TestSunLevel(t *testing.T) {
	c := BrightnessConfig{}.resolved() // night 20, day 255, twilight 45 min
	rise, set, ok := sunTimes(lonLat, lonLon, jun21)
	if !ok {
		t.Fatal("no sun times")
	}
	tw := time.Duration(c.TwilightMinutes) * time.Minute
	cases := []struct {
		name      string
		at        time.Time
		want      int
		wantNight bool
	}{
		{"midday", jun21.Add(12 * time.Hour), 255, false},
		{"midnight", jun21, 20, true},
		{"before ramp starts", rise.Add(-tw - time.Minute), 20, true},
		{"ramp start", rise.Add(-tw), 20, true},
		{"ramp midpoint to sunrise", rise.Add(-tw / 2), 138, true},
		{"sunrise", rise, 255, false},
		{"just before sunset", set.Add(-time.Minute), 255, false},
		{"just after sunset", set.Add(time.Second), 255, true},
		{"ramp midpoint after sunset", set.Add(tw / 2), 138, true},
		{"ramp end", set.Add(tw), 20, true},
		{"after ramp", set.Add(tw + time.Minute), 20, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, night := sunLevel(c, lonLat, lonLon, tc.at)
			if abs(got-tc.want) > 1 || night != tc.wantNight {
				t.Errorf("sunLevel = %d,%v want %d,%v", got, night, tc.want, tc.wantNight)
			}
		})
	}
}

// Western longitudes cross the UTC date line between sunrise and sunset;
// the level must still be right in the local evening (next UTC day).
func TestSunLevelAcrossUTCDate(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	// Los Angeles, 2026-06-21 20:00 local (PDT) = 03:00Z on the 22nd: night.
	if got, night := sunLevel(c, 34.05, -118.24, time.Date(2026, 6, 22, 5, 0, 0, 0, time.UTC)); !night || got != 20 {
		t.Errorf("LA night = %d,%v", got, night)
	}
	// 19:00 local = 02:00Z the 22nd: sun is still up.
	if got, night := sunLevel(c, 34.05, -118.24, time.Date(2026, 6, 22, 2, 0, 0, 0, time.UTC)); night || got != 255 {
		t.Errorf("LA evening = %d,%v", got, night)
	}
}

func TestSunLevelPolarNight(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	dec21 := time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC)
	if got, night := sunLevel(c, 78.2, 15.6, dec21); !night || got != 20 {
		t.Errorf("polar night = %d,%v, want 20,true", got, night)
	}
}

func TestSunLevelPolar(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	// Svalbard midsummer: midnight sun, no rise/set. Stays day.
	if got, night := sunLevel(c, 78.2, 15.6, jun21.Add(23*time.Hour)); night || got != 255 {
		t.Errorf("polar day = %d,%v", got, night)
	}
}

func luxAt(lux float64, at time.Time) *luxSample { return &luxSample{Lux: lux, At: at} }

func TestDecideSources(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	noon := jun21.Add(12 * time.Hour)
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	cases := []struct {
		name       string
		sample     *luxSample
		geo        brightnessGeo
		now        time.Time
		wantLevel  int
		wantSource string
		wantNight  bool
	}{
		{"fresh lux wins over sun", luxAt(0.5, noon), geo, noon, 10, "lux", false},
		{"fresh lux bright", luxAt(1000, noon), geo, noon, 255, "lux", false},
		{"lux at night keeps night flag from sun", luxAt(1000, jun21), geo, jun21, 255, "lux", true},
		{"lux without location", luxAt(0.5, noon), brightnessGeo{}, noon, 10, "lux", false},
		{"stale sample falls back to sun day", luxAt(0.5, noon.Add(-time.Hour)), geo, noon, 255, "sun", false},
		{"stale sample falls back to sun night", luxAt(500, jun21.Add(-time.Hour)), geo, jun21, 20, "sun", true},
		{"no clock falls back to sun", nil, geo, jun21, 20, "sun", true},
		{"no clock, no location", nil, brightnessGeo{}, noon, 255, "default", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := decideBrightness(c, brightnessState{}, tc.sample, tc.geo, tc.now)
			if out.Level != tc.wantLevel || out.Source != tc.wantSource || out.Night != tc.wantNight {
				t.Errorf("got %+v, want %d/%s/night=%v", out, tc.wantLevel, tc.wantSource, tc.wantNight)
			}
		})
	}
}

func TestDecideSmoothsAndHolds(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	c.EMAAlpha = 0.5
	c.Hysteresis = 8
	geo := brightnessGeo{}
	t0 := jun21.Add(12 * time.Hour)
	st := brightnessState{}

	step := func(lux float64, at time.Time) brightnessOut {
		var out brightnessOut
		out, st = decideBrightness(c, st, luxAt(lux, at), geo, at)
		return out
	}

	first := step(200, t0)
	if first.Level != 255 {
		t.Fatalf("first = %d, want 255 (seeded from first sample)", first.Level)
	}
	// One dark reading is halved by the EMA (200 -> 100 lux), not obeyed.
	second := step(0.5, t0.Add(30*time.Second))
	if second.Level < 200 {
		t.Errorf("EMA should damp a single dim sample: level %d", second.Level)
	}
	// Re-asking with the same sample timestamp must not feed the EMA twice.
	again, st2 := decideBrightness(c, st, luxAt(0.5, t0.Add(30*time.Second)), geo, t0.Add(31*time.Second))
	if again.Level != second.Level || st2.EMA != st.EMA {
		t.Errorf("same sample re-applied: %d vs %d, ema %v vs %v", again.Level, second.Level, st2.EMA, st.EMA)
	}
	// Sustained dark converges to the floor.
	var last brightnessOut
	for i := 2; i < 30; i++ {
		last = step(0.5, t0.Add(time.Duration(i)*30*time.Second))
	}
	if last.Level != 10 {
		t.Errorf("sustained dark = %d, want floor 10", last.Level)
	}
	// Jitter around a steady level (within band) does not move it.
	st = brightnessState{}
	base := step(40, t0)
	for i, lux := range []float64{42, 38, 41, 39, 43} {
		got := step(lux, t0.Add(time.Duration(i+1)*30*time.Second))
		if got.Level != base.Level {
			t.Errorf("jitter moved level %d -> %d at sample %d", base.Level, got.Level, i)
		}
	}
}

func TestDecideStaleResetsEMA(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	noon := jun21.Add(12 * time.Hour)
	_, st := decideBrightness(c, brightnessState{}, luxAt(200, noon), geo, noon)
	if !st.HasEMA {
		t.Fatal("ema not seeded")
	}
	_, st = decideBrightness(c, st, nil, geo, noon.Add(time.Hour))
	if st.HasEMA || st.HasLevel {
		t.Errorf("state should reset when clock is gone: %+v", st)
	}
	// Coming back dark starts from the new reading, not the old bright EMA.
	out, _ := decideBrightness(c, st, luxAt(0.5, noon.Add(2*time.Hour)), geo, noon.Add(2*time.Hour))
	if out.Level != 10 {
		t.Errorf("after reset level = %d, want 10", out.Level)
	}
}

func TestBrightnessAtMatchesTickWithoutChangingState(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	t0 := jun21.Add(12 * time.Hour)
	st := brightnessState{}
	for i, lux := range []float64{200, 0.5, 0.5, 40, 42, 1000} {
		at := t0.Add(time.Duration(i) * time.Minute)
		var out brightnessOut
		out, st = decideBrightness(c, st, luxAt(lux, at), geo, at)
		before := st
		if got := brightnessAt(c, st, geo, at.Add(30*time.Second)); got != out {
			t.Errorf("step %d: read %+v, tick %+v", i, got, out)
		}
		if st != before {
			t.Fatalf("step %d: read changed the state", i)
		}
	}
	late := t0.Add(time.Hour)
	if got, want := brightnessAt(c, st, geo, late), (brightnessOut{Level: c.DayLevel, Source: "sun"}); got != want {
		t.Errorf("read past stale_seconds = %+v, want %+v", got, want)
	}
}

func TestBrightnessAtFollowsConfigWithoutATick(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	at := jun21.Add(12 * time.Hour)
	_, st := decideBrightness(c, brightnessState{}, luxAt(1000, at), brightnessGeo{}, at)
	c.Ceiling = 200
	if got := brightnessAt(c, st, brightnessGeo{}, at); got.Level != 200 {
		t.Errorf("level after lowering the ceiling = %d, want 200", got.Level)
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ---- through the real routes ----

func lightClock(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrightnessEndpointLuxFromClock(t *testing.T) {
	app := newPomodoroApp(t)
	clock := lightClock(t, `{"version":"1.1.1","lightLevel":1000}`)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.tickBrightness(t.Context(), time.Now())
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	status, body := getOpen(t, srv, "/v1/display/brightness") // no token
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body["source"] != "lux" || body["level"] != float64(255) || body["night"] != false || len(body) != 3 {
		t.Errorf("body = %v", body)
	}
}

func TestBrightnessEndpointNeitherProbesNorAdvancesFilter(t *testing.T) {
	app := newPomodoroApp(t)
	var hits atomic.Int64
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"version":"1.1.1","lightLevel":40}`))
	}))
	t.Cleanup(clock.Close)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.tickBrightness(t.Context(), time.Now())
	app.clockProbe.mu.Lock()
	app.clockProbe.at = time.Time{}
	app.clockProbe.mu.Unlock()
	app.brightness.mu.Lock()
	st := app.brightness.st
	app.brightness.mu.Unlock()
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	for range 3 {
		if _, body := getOpen(t, srv, "/v1/display/brightness"); body["source"] != "lux" {
			t.Fatalf("body = %v", body)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("clock probes = %d, want only the tick's 1", got)
	}
	app.brightness.mu.Lock()
	defer app.brightness.mu.Unlock()
	if app.brightness.st != st {
		t.Errorf("GET changed the filter state: %+v -> %+v", st, app.brightness.st)
	}
}

func TestClockProbeReleasesLockDuringRequest(t *testing.T) {
	app := newPomodoroApp(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		_, _ = w.Write([]byte(`{"version":"1.1.1","lightLevel":40}`))
	}))
	t.Cleanup(clock.Close)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	done := make(chan *clockDeviceOut)
	go func() { done <- app.probeClockHealth(context.Background(), time.Now()) }()
	<-arrived
	locked := app.clockProbe.mu.TryLock()
	if locked {
		app.clockProbe.mu.Unlock()
	}
	close(release)
	if dev := <-done; dev == nil || !dev.Reachable {
		t.Fatalf("probe = %+v, want reachable", dev)
	}
	if !locked {
		t.Fatal("clockProbe.mu held across the clock request")
	}
}

func TestBrightnessEndpointFallsBackToSunWhenClockDown(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) {
		c.AWTRIX.HTTPBaseURL = closedURL(t)
		c.Weather.Latitude, c.Weather.Longitude = lonLat, lonLon
	})
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/display/brightness")
	if body["source"] != "sun" {
		t.Errorf("source = %v, want sun", body["source"])
	}
}

func TestBrightnessEndpointDefaultWithoutClockOrLocation(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = closedURL(t) })
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/display/brightness")
	if body["source"] != "default" || body["level"] != float64(255) || body["night"] != false {
		t.Errorf("body = %v", body)
	}
}

func TestBrightnessEndpointClockWithoutSensor(t *testing.T) {
	app := newPomodoroApp(t)
	clock := lightClock(t, `{"version":"1.1.1"}`)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	app.tickBrightness(t.Context(), time.Now())
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/display/brightness")
	if body["source"] == "lux" {
		t.Errorf("no lightLevel reported but source = lux: %v", body)
	}
}

func TestBrightnessConfigMergePut(t *testing.T) {
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.Auth.StatusToken = testToken })
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	put := func(body string, authed bool) (int, BrightnessConfig) {
		req, _ := http.NewRequest("PUT", srv.URL+"/v1/brightness/config", strings.NewReader(body))
		if authed {
			req = authedRequest(t, "PUT", srv.URL+"/v1/brightness/config", body)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var d BrightnessConfig
		_ = json.NewDecoder(resp.Body).Decode(&d)
		return resp.StatusCode, d
	}

	if code, _ := put(`{"floor":30}`, false); code != http.StatusUnauthorized {
		t.Fatalf("PUT without token = %d, want 401", code)
	}
	code, d := put(`{"floor":30,"night_level":40}`, true)
	if code != http.StatusOK || d.Floor != 30 || d.NightLevel != 40 || d.Ceiling != 255 || d.DayLevel != 255 {
		t.Fatalf("merge PUT = %d %+v", code, d)
	}
	// Raising the floor above the stored night level is rejected, not clamped.
	if code, _ := put(`{"floor":50}`, true); code != http.StatusBadRequest {
		t.Errorf("invalid PUT = %d, want 400", code)
	}
	if got := app.cfg.Load().Brightness.resolved().Floor; got != 30 {
		t.Errorf("invalid PUT changed floor to %d", got)
	}
}

func TestDecideLastGoodSampleStaleBoundary(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	noon := jun21.Add(12 * time.Hour)
	stale := time.Duration(c.StaleSeconds) * time.Second
	_, st := decideBrightness(c, brightnessState{}, luxAt(0.5, noon), geo, noon)

	out, st2 := decideBrightness(c, st, nil, geo, noon.Add(stale))
	if out.Source != "lux" || out.Level != 10 || st2.EMA != st.EMA || !st2.HasEMA {
		t.Errorf("exactly stale_seconds old: %+v ema %v, want lux 10 with the EMA kept", out, st2.EMA)
	}
	out, _ = decideBrightness(c, st, nil, geo, noon.Add(stale+time.Nanosecond))
	if out.Source != "sun" {
		t.Errorf("just past stale_seconds: %+v, want sun", out)
	}
}

func TestDecideOlderSampleDoesNotReplaceLastGood(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	stale := time.Duration(c.StaleSeconds) * time.Second
	t0 := jun21.Add(12 * time.Hour)
	newer := t0.Add(stale)
	_, st := decideBrightness(c, brightnessState{}, luxAt(0.5, newer), geo, newer)
	_, st = decideBrightness(c, st, luxAt(0.5, t0), geo, newer)

	now := newer.Add(stale - time.Second)
	out, _ := decideBrightness(c, st, nil, geo, now)
	if out.Source != "lux" {
		t.Errorf("older sample displaced the newer one: %+v, want lux (newer is %s old)", out, stale-time.Second)
	}
}

func TestDecideEMAGapBoundary(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	geo := brightnessGeo{}
	stale := time.Duration(c.StaleSeconds) * time.Second
	t0 := jun21.Add(12 * time.Hour)
	_, st := decideBrightness(c, brightnessState{}, luxAt(100, t0), geo, t0)

	at := t0.Add(stale)
	_, blended := decideBrightness(c, st, luxAt(10, at), geo, at)
	want := c.EMAAlpha*10 + (1-c.EMAAlpha)*100
	if blended.EMA != want {
		t.Errorf("gap == stale_seconds: ema %v, want blended %v", blended.EMA, want)
	}
	at = t0.Add(stale + time.Nanosecond)
	_, reseeded := decideBrightness(c, st, luxAt(10, at), geo, at)
	if reseeded.EMA != 10 {
		t.Errorf("gap > stale_seconds: ema %v, want reseed 10", reseeded.EMA)
	}
}
