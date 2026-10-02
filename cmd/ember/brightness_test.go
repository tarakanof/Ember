package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"stale too small", func(c *BrightnessConfig) { c.StaleSeconds = 1 }, false},
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
	c := BrightnessConfig{}.resolved() // dark 5 lux, bright 300 lux, 10..255
	cases := []struct {
		lux  float64
		want int
	}{
		{-3, 10}, {0, 10}, {5, 10}, {300, 255}, {5000, 255},
		{39, 133}, // geometric midpoint of 5 and 300 is ~38.7 lux: halfway up
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
		{"zero band follows", 100, true, 101, 0, 10, 255, 101},
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

func TestSunLevelZeroTwilightIsAStep(t *testing.T) {
	c := BrightnessConfig{}.resolved()
	c.TwilightMinutes = 0
	_, set, _ := sunTimes(lonLat, lonLon, jun21)
	if got, _ := sunLevel(c, lonLat, lonLon, set.Add(time.Minute)); got != 20 {
		t.Errorf("after sunset = %d, want night level 20", got)
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
		{"fresh lux wins over sun", luxAt(5, noon), geo, noon, 10, "lux", false},
		{"fresh lux bright", luxAt(1000, noon), geo, noon, 255, "lux", false},
		{"lux at night keeps night flag from sun", luxAt(1000, jun21), geo, jun21, 255, "lux", true},
		{"lux without location", luxAt(5, noon), brightnessGeo{}, noon, 10, "lux", false},
		{"stale sample falls back to sun day", luxAt(5, noon.Add(-time.Hour)), geo, noon, 255, "sun", false},
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

	first := step(300, t0)
	if first.Level != 255 {
		t.Fatalf("first = %d, want 255 (seeded from first sample)", first.Level)
	}
	// One dark reading is halved by the EMA (300 -> 152.5 lux), not obeyed.
	second := step(5, t0.Add(30*time.Second))
	if second.Level < 200 {
		t.Errorf("EMA should damp a single dim sample: level %d", second.Level)
	}
	// Re-asking with the same sample timestamp must not feed the EMA twice.
	again, st2 := decideBrightness(c, st, luxAt(5, t0.Add(30*time.Second)), geo, t0.Add(31*time.Second))
	if again.Level != second.Level || st2.EMA != st.EMA {
		t.Errorf("same sample re-applied: %d vs %d, ema %v vs %v", again.Level, second.Level, st2.EMA, st.EMA)
	}
	// Sustained dark converges to the floor.
	var last brightnessOut
	for i := 2; i < 30; i++ {
		last = step(5, t0.Add(time.Duration(i)*30*time.Second))
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
	_, st := decideBrightness(c, brightnessState{}, luxAt(300, noon), geo, noon)
	if !st.HasEMA {
		t.Fatal("ema not seeded")
	}
	_, st = decideBrightness(c, st, nil, geo, noon.Add(time.Hour))
	if st.HasEMA || st.HasLevel {
		t.Errorf("state should reset when clock is gone: %+v", st)
	}
	// Coming back dark starts from the new reading, not the old bright EMA.
	out, _ := decideBrightness(c, st, luxAt(5, noon.Add(2*time.Hour)), geo, noon.Add(2*time.Hour))
	if out.Level != 10 {
		t.Errorf("after reset level = %d, want 10", out.Level)
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
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	_, body := getOpen(t, srv, "/v1/display/brightness")
	if body["source"] == "lux" {
		t.Errorf("no lightLevel reported but source = lux: %v", body)
	}
}

func TestBrightnessConfigMergePut(t *testing.T) {
	app := newPomodoroApp(t)
	var d BrightnessConfig
	rec := httptest.NewRecorder()
	app.handleBrightnessConfigGet(rec, httptest.NewRequest("GET", "/v1/brightness/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || d.Floor != 10 || d.Ceiling != 255 {
		t.Fatalf("GET = %s (%v)", rec.Body, err)
	}

	rec = httptest.NewRecorder()
	app.handleBrightnessConfigPut(rec, httptest.NewRequest("PUT", "/v1/brightness/config", strings.NewReader(`{"floor":30,"night_level":40}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	got := app.cfg.Load().Brightness.resolved()
	if got.Floor != 30 || got.Ceiling != 255 || got.NightLevel != 40 || got.DayLevel != 255 {
		t.Errorf("after merge = %+v", got)
	}

	// Raising the floor above the stored night level is rejected, not clamped.
	rec = httptest.NewRecorder()
	app.handleBrightnessConfigPut(rec, httptest.NewRequest("PUT", "/v1/brightness/config", strings.NewReader(`{"floor":50}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid PUT = %d, want 400", rec.Code)
	}
	if app.cfg.Load().Brightness.resolved().Floor != 30 {
		t.Error("invalid PUT changed the config")
	}
}
