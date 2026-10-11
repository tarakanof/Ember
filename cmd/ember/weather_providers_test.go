package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const openMeteoFacesBody = `{"utc_offset_seconds":7200,
"current":{"time":1782036000,"temperature_2m":12.5,"weather_code":61,"cloud_cover":94,"uv_index":2.6,"wind_speed_10m":21.2,"wind_gusts_10m":38.4,"wind_direction_10m":225},
"minutely_15":{"time":[1782036000,1782036900,1782037800,1782038700],"precipitation":[0.0,0.3,null,0.5],"snowfall":[0,0,0,0.2]},
"hourly":{"time":[1782036000],"temperature_2m":[12.5]},
"daily":{"time":[1781992800,1782079200],"weather_code":[61,3],"temperature_2m_max":[14.2,16],"temperature_2m_min":[9.1,7],
"precipitation_sum":[2.1,0],"precipitation_probability_max":[83,null],"uv_index_max":[4.1,null],"wind_gusts_10m_max":[41,30]}}`

const metCompleteBody = `{"properties":{"timeseries":[
{"time":"2026-06-21T20:00:00Z","data":{"instant":{"details":{"air_temperature":14,"cloud_area_fraction":80,"ultraviolet_index_clear_sky":0.5,"wind_speed":5,"wind_speed_of_gust":10,"wind_from_direction":225}},
 "next_1_hours":{"summary":{"symbol_code":"lightrain"},"details":{"precipitation_amount":0.5}},
 "next_6_hours":{"summary":{"symbol_code":"rain"},"details":{"air_temperature_min":9,"air_temperature_max":14,"precipitation_amount":2,"probability_of_precipitation":60}}}},
{"time":"2026-06-21T21:00:00Z","data":{"instant":{"details":{"air_temperature":13,"wind_speed_of_gust":12}},
 "next_1_hours":{"summary":{"symbol_code":"lightsleet"},"details":{"precipitation_amount":1.0}}}},
{"time":"2026-06-21T22:00:00Z","data":{"instant":{"details":{"air_temperature":12}},
 "next_1_hours":{"summary":{"symbol_code":"cloudy"},"details":{"precipitation_amount":0}},
 "next_6_hours":{"summary":{"symbol_code":"cloudy"},"details":{"air_temperature_min":8,"air_temperature_max":12,"precipitation_amount":0.6,"probability_of_precipitation":20}}}},
{"time":"2026-06-22T00:00:00Z","data":{"instant":{"details":{"air_temperature":11}},
 "next_6_hours":{"summary":{"symbol_code":"heavyrainandthunder"},"details":{"air_temperature_min":7,"air_temperature_max":15,"precipitation_amount":3,"probability_of_precipitation":70}}}},
{"time":"2026-06-22T06:00:00Z","data":{"instant":{"details":{"air_temperature":15}},
 "next_6_hours":{"summary":{"symbol_code":"rain"},"details":{"air_temperature_min":10,"air_temperature_max":18,"precipitation_amount":1,"probability_of_precipitation":40}}}},
{"time":"2026-06-22T12:00:00Z","data":{"instant":{"details":{"air_temperature":18}}}}
]}}`

var metT0 = time.Date(2026, 6, 21, 20, 0, 0, 0, time.UTC)

func approx(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }

func TestFetchOpenMeteoRequestsAndParsesFaceData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		want := map[string]string{
			"current":              "temperature_2m,weather_code,cloud_cover,uv_index,wind_speed_10m,wind_gusts_10m,wind_direction_10m",
			"minutely_15":          "precipitation,snowfall",
			"forecast_minutely_15": "48",
			"daily":                "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,uv_index_max,wind_gusts_10m_max",
			"forecast_days":        "4",
			"hourly":               "temperature_2m",
			"timeformat":           "unixtime",
		}
		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, q.Get(k), v)
			}
		}
		_, _ = w.Write([]byte(openMeteoFacesBody))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.openMeteoBase = srv.URL
	obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 52.5, Longitude: 13.4})
	if err != nil {
		t.Fatal(err)
	}
	n := obs.Now
	if !approx(n.Cloud, 94) || !approx(n.UV, 2.6) || !approx(n.WindKmh, 21.2) || !approx(n.GustKmh, 38.4) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v", n)
	}
	if obs.PrecipStep != 15*time.Minute || len(obs.Precip) != 3 {
		t.Fatalf("precip step %v steps %+v, want 15m and 3 steps (the null one left out)", obs.PrecipStep, obs.Precip)
	}
	if p := obs.Precip[1]; p.At.Unix() != 1782036000 || math.Abs(p.MMh-1.2) > 1e-9 || p.Snow {
		t.Errorf("step 1 = %+v, want 1.2 mm/h rain (0.3 mm in the 15 min before 1782036900)", p)
	}
	if p := obs.Precip[2]; p.At.Unix() != 1782037800 || math.Abs(p.MMh-2) > 1e-9 || !p.Snow {
		t.Errorf("step 2 = %+v, want 2 mm/h snow starting 15 min before its 1782038700 stamp", p)
	}
	if len(obs.Days) != 2 {
		t.Fatalf("days = %+v, want 2", obs.Days)
	}
	d0, d1 := obs.Days[0], obs.Days[1]
	if d0.Start.Unix() != 1781992800 || d0.Cond != render.WeatherRain || d0.Code != "61" || !approx(d0.MinC, 9.1) || !approx(d0.MaxC, 14.2) ||
		!approx(d0.PrecipMM, 2.1) || !approx(d0.Pop, 83) || !approx(d0.UVMax, 4.1) || !approx(d0.GustMaxKmh, 41) {
		t.Errorf("day 0 = %+v", d0)
	}
	if d1.Cond != render.WeatherClouds || d1.Code != "3" || d1.Pop != nil || d1.UVMax != nil {
		t.Errorf("day 1 = %+v, want clouds with pop and uv left out", d1)
	}
}

func TestFetchMetNoUsesCompleteAndAggregatesDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/locationforecast/2.0/complete") {
			t.Errorf("path = %s, want the complete product", r.URL.Path)
		}
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.metNoBase = srv.URL
	res, err := wf.fetchMetNo(t.Context(), WeatherConfig{Latitude: 59.9, Longitude: 10.7}, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	obs := res.Obs
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Errorf("tz = (%v, %d), want the hint's 7200", obs.TZKnown, obs.TZOffsetSeconds)
	}
	n := obs.Now
	if !approx(n.Cloud, 80) || !approx(n.UV, 0.5) || !approx(n.WindKmh, 18) || !approx(n.GustKmh, 36) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v, want m/s turned into km/h", n)
	}
	if obs.PrecipStep != time.Hour || len(obs.Precip) != 2 {
		t.Fatalf("precip = %v %+v, want the two leading 1-h steps", obs.PrecipStep, obs.Precip)
	}
	if p := obs.Precip[1]; p.MMh != 1 || !p.Snow {
		t.Errorf("sleet step = %+v, want 1 mm/h snow kind", p)
	}
	if len(obs.Days) != 2 {
		t.Fatalf("days = %+v, want 2 local days", obs.Days)
	}
	a, b := obs.Days[0], obs.Days[1]
	if a.Start != time.Date(2026, 6, 20, 22, 0, 0, 0, time.UTC) || a.Cond != render.WeatherSnow || a.Code != "lightsleet" ||
		!approx(a.MinC, 13) || !approx(a.MaxC, 14) || !approx(a.PrecipMM, 1.5) || a.Pop != nil || !approx(a.UVMax, 0.5) || !approx(a.GustMaxKmh, 43.2) {
		t.Errorf("day 0 = %+v", a)
	}
	if b.Start != time.Date(2026, 6, 21, 22, 0, 0, 0, time.UTC) || b.Cond != render.WeatherStorm || b.Code != "heavyrainandthunder" ||
		!approx(b.MinC, 7) || !approx(b.MaxC, 18) || b.PrecipMM == nil || math.Abs(*b.PrecipMM-4.2) > 1e-9 || !approx(b.Pop, 70) {
		t.Errorf("day 1 = %+v", b)
	}
}

func TestFetchMetNoWithoutOffsetFallsBackToLongitudeDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.metNoBase = srv.URL
	res, err := wf.fetchMetNo(t.Context(), WeatherConfig{Latitude: 59.9, Longitude: 10.7}, fetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Obs.TZKnown || len(res.Obs.Days) == 0 || res.Obs.Days[0].Start != time.Date(2026, 6, 20, 23, 0, 0, 0, time.UTC) {
		t.Fatalf("tz known %v, days %+v, want day 0 at local midnight for UTC+1 (lon 10.7)", res.Obs.TZKnown, res.Obs.Days)
	}
}

func TestFetchAirQualityReadsUTCOffset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"utc_offset_seconds":7200,"current":{"european_aqi":18}}`))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.airQualityBase = srv.URL
	obs, err := wf.fetchAirQuality(t.Context(), WeatherConfig{Latitude: 59.9, Longitude: 10.7})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Fatalf("air tz = (%v, %d), want (true, 7200)", obs.TZKnown, obs.TZOffsetSeconds)
	}
}

type metStub struct {
	mu           sync.Mutex
	hits         atomic.Int32
	ims          []string
	lats         []string
	lastModified string
	expires      time.Time
	notModified  bool
	airFail      atomic.Bool
	onRequest    func()
}

func newMetCacheApp(t *testing.T, st *metStub, airOn bool) *App {
	t.Helper()
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		if st.onRequest != nil {
			st.onRequest()
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.ims = append(st.ims, r.Header.Get("If-Modified-Since"))
		st.lats = append(st.lats, r.URL.Query().Get("lat"))
		w.Header().Set("Last-Modified", st.lastModified)
		w.Header().Set("Expires", st.expires.UTC().Format(http.TimeFormat))
		if st.notModified && r.Header.Get("If-Modified-Since") == st.lastModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	t.Cleanup(met.Close)
	air := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st.airFail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"utc_offset_seconds":10800,"current":{"european_aqi":18}}`))
	}))
	t.Cleanup(air.Close)
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Provider = "met-no"
	cfg.Weather.Latitude, cfg.Weather.Longitude = 59.9, 10.7
	cfg.Weather.PopupOnChange = boolPtr(false)
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.SunPopups = boolPtr(false)
	cfg.Weather.SevereAlert = boolPtr(false)
	cfg.Weather.AirTile = boolPtr(airOn)
	cfg.Weather.AirPopupThreshold = 0
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	app.weatherFetcher = newWeatherFetcher()
	app.weatherFetcher.metNoBase = met.URL
	app.weatherFetcher.airQualityBase = air.URL
	return app
}

func TestMetNoWaitsForExpiresThenRevalidates(t *testing.T) {
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(25 * time.Minute)}
	app := newMetCacheApp(t, st, false)
	ctx := context.Background()

	app.pollWeather(ctx, t0)
	if st.hits.Load() != 1 || st.ims[0] != "" {
		t.Fatalf("first poll: hits %d, If-Modified-Since %q, want 1 and none", st.hits.Load(), st.ims)
	}

	t1 := t0.Add(10 * time.Minute)
	app.pollWeather(ctx, t1)
	if st.hits.Load() != 1 {
		t.Fatalf("polled before Expires: %d requests, want 1", st.hits.Load())
	}
	app.weather.mu.RLock()
	last := app.weather.lastFetch
	app.weather.mu.RUnlock()
	if !last.Equal(t1) {
		t.Errorf("lastFetch = %v, want the skipped poll's %v so the cadence holds", last, t1)
	}

	st.mu.Lock()
	st.notModified = true
	st.expires = t0.Add(60 * time.Minute)
	st.mu.Unlock()
	seq, _ := app.changes.subscribe()
	t2 := t0.Add(30 * time.Minute)
	app.pollWeather(ctx, t2)
	if st.hits.Load() != 2 || st.ims[1] != st.lastModified {
		t.Fatalf("after Expires: hits %d, If-Modified-Since %q, want 2 and %q", st.hits.Load(), st.ims, st.lastModified)
	}
	if app.changes.since(seq)&topicWeather == 0 {
		t.Error("a 304 that renewed the observation did not notify")
	}
	wx := app.weather.state(app.cfg.Load().Weather, t2)
	if !wx.HaveObs || wx.Obs.TempC != 14 || !wx.Obs.FetchedAt.Equal(t2) || !wx.Fresh {
		t.Fatalf("after 304: have %v temp %v fetched %v fresh %v, want the kept observation renewed at %v", wx.HaveObs, wx.Obs.TempC, wx.Obs.FetchedAt, wx.Fresh, t2)
	}
	app.pollWeather(ctx, t0.Add(50*time.Minute))
	if st.hits.Load() != 2 {
		t.Errorf("polled before the 304's Expires: %d requests, want 2", st.hits.Load())
	}
}

func TestMetNoCurrentValuesFollowTheKeptSeries(t *testing.T) {
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: metT0.Add(25 * time.Minute), notModified: true}
	app := newMetCacheApp(t, st, false)
	app.pollWeather(context.Background(), metT0)
	t1 := metT0.Add(61 * time.Minute)
	app.pollWeather(context.Background(), t1)
	if st.hits.Load() != 2 || st.ims[1] == "" {
		t.Fatalf("setup: hits %d If-Modified-Since %q, want a 304 at 21:01", st.hits.Load(), st.ims)
	}
	wx := app.weather.state(app.cfg.Load().Weather, t1)
	o := wx.Obs
	if !wx.Fresh || o.TempC != 13 || o.Condition != render.WeatherSnow || o.ConditionCode != "lightsleet" ||
		!o.ObservedAt.Equal(metT0.Add(time.Hour)) || !approx(o.Now.GustKmh, 43.2) || o.Now.Cloud != nil {
		t.Fatalf("after the 21:01 304: fresh %v %v°C %s/%s observed %v now %+v; want the 21:00 entry (13°C lightsleet, gust 43.2)",
			wx.Fresh, o.TempC, o.Condition, o.ConditionCode, o.ObservedAt, o.Now)
	}
	if o := app.weather.state(app.cfg.Load().Weather, metT0.Add(2*time.Hour+5*time.Minute)).Obs; o.TempC != 12 || o.ConditionCode != "cloudy" {
		t.Errorf("at 22:05 without a fetch: %v°C %s, want the 22:00 entry", o.TempC, o.ConditionCode)
	}
}

func TestMetNoAfterAFailedProviderRoundTripFetchesWithoutValidators(t *testing.T) {
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(25 * time.Minute), notModified: true}
	app := newMetCacheApp(t, st, false)
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(om.Close)
	app.weatherFetcher.openMeteoBase = om.URL
	ctx := context.Background()
	setProvider := func(p string) { app.updateConfig(func(c *Config) { c.Weather.Provider = p }) }

	app.pollWeather(ctx, t0)
	setProvider("open-meteo")
	app.pollWeather(ctx, t0.Add(time.Minute))
	setProvider("met-no")
	t2 := t0.Add(2 * time.Minute)
	app.pollWeather(ctx, t2)

	st.mu.Lock()
	ims := slices.Clone(st.ims)
	st.mu.Unlock()
	if len(ims) != 2 || ims[1] != "" {
		t.Fatalf("met-no requests carried If-Modified-Since %q, want the second without one", ims)
	}
	if wx := app.weather.state(app.cfg.Load().Weather, t2); !wx.HaveObs || wx.Obs.Condition != render.WeatherRain {
		t.Fatalf("after met-no -> open-meteo -> met-no: have %v obs %+v, want MET's observation", wx.HaveObs, wx.Obs)
	}
}

func TestMetNo304AfterAProviderSwitchIsDropped(t *testing.T) {
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(-time.Minute), notModified: true}
	app := newMetCacheApp(t, st, false)
	ctx := context.Background()
	app.pollWeather(ctx, t0)

	st.onRequest = func() { app.updateConfig(func(c *Config) { c.Weather.Provider = "open-meteo" }) }
	seq, _ := app.changes.subscribe()
	app.pollWeather(ctx, t0.Add(10*time.Minute))

	if st.hits.Load() != 2 || st.ims[1] == "" {
		t.Fatalf("setup: hits %d If-Modified-Since %q, want a revalidation", st.hits.Load(), st.ims)
	}
	app.weather.mu.RLock()
	last, fetched := app.weather.lastFetch, app.weather.obs.FetchedAt
	app.weather.mu.RUnlock()
	if !last.Equal(t0) || !fetched.Equal(t0) {
		t.Errorf("a 304 for met-no landing after the switch renewed the store: lastFetch %v FetchedAt %v, want %v", last, fetched, t0)
	}
	if app.changes.since(seq)&topicWeather != 0 {
		t.Error("a 304 for the old provider notified")
	}
}

func TestMetNoLocationChangeRefetchesWithoutValidators(t *testing.T) {
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(25 * time.Minute), notModified: true}
	app := newMetCacheApp(t, st, false)
	ctx := context.Background()
	app.pollWeather(ctx, t0)
	if st.hits.Load() != 1 {
		t.Fatalf("first poll: %d requests, want 1", st.hits.Load())
	}

	t1 := t0.Add(11 * time.Minute)
	app.updateConfig(func(c *Config) { c.Weather.Latitude, c.Weather.Longitude = 52.52, 13.405 })
	if wx := app.weather.state(app.cfg.Load().Weather, t1); wx.HaveObs {
		t.Fatalf("Oslo's observation served for Berlin before the refetch: %+v", wx.Obs)
	}
	app.pollWeather(ctx, t1)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.hits.Load() != 2 || st.ims[1] != "" || st.lats[1] != "52.5200" {
		t.Fatalf("after the move: hits %d, If-Modified-Since %q, lat %q; want a full fetch for 52.5200 before Oslo's Expires", st.hits.Load(), st.ims, st.lats)
	}
	if wx := app.weather.state(app.cfg.Load().Weather, t1); !wx.HaveObs || !wx.Obs.FetchedAt.Equal(t1) {
		t.Errorf("Berlin observation = have %v fetched %v, want fetched %v", wx.HaveObs, wx.Obs.FetchedAt, t1)
	}
}

func TestMetNoTakesUTCOffsetFromAirCall(t *testing.T) {
	for _, airOn := range []bool{true, false} {
		st := &metStub{expires: time.Now().Add(-time.Minute)}
		app := newMetCacheApp(t, st, airOn)
		app.pollWeather(context.Background(), time.Now())
		wx := app.weather.state(app.cfg.Load().Weather, time.Now())
		if !wx.HaveObs {
			t.Fatalf("air %v: no observation", airOn)
		}
		if airOn != wx.Obs.TZKnown || (airOn && wx.Obs.TZOffsetSeconds != 10800) {
			t.Errorf("air %v: tz = (%v, %d), want the air call's 10800 only with air on", airOn, wx.Obs.TZKnown, wx.Obs.TZOffsetSeconds)
		}
	}
}

func TestMetNoDropsTheAirOffsetOnceAirIsOffOrStale(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		spoil func(*App, *metStub)
	}{
		{"air turned off", 10 * time.Minute, func(app *App, _ *metStub) {
			app.updateConfig(func(c *Config) { c.Weather.AirTile = boolPtr(false) })
		}},
		{"air failing past staleness", 40 * time.Minute, func(_ *App, st *metStub) { st.airFail.Store(true) }},
		{"location changed while air fails", 10 * time.Minute, func(app *App, st *metStub) {
			st.airFail.Store(true)
			app.updateConfig(func(c *Config) { c.Weather.Latitude, c.Weather.Longitude = 40.7, -74 })
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t0 := metT0
			st := &metStub{expires: t0.Add(-time.Minute)}
			app := newMetCacheApp(t, st, true)
			app.pollWeather(context.Background(), t0)
			if wx := app.weather.state(app.cfg.Load().Weather, t0); !wx.Obs.TZKnown || wx.Obs.TZOffsetSeconds != 10800 {
				t.Fatalf("setup: tz = (%v, %d), want the air call's 10800", wx.Obs.TZKnown, wx.Obs.TZOffsetSeconds)
			}
			c.spoil(app, st)
			t1 := t0.Add(c.after)
			app.pollWeather(context.Background(), t1)
			wx := app.weather.state(app.cfg.Load().Weather, t1)
			if st.hits.Load() != 2 || !wx.HaveObs || wx.Obs.TZKnown {
				t.Errorf("hits %d, have %v, tz = (%v, %d); want a refetch on the longitude fallback", st.hits.Load(), wx.HaveObs, wx.Obs.TZKnown, wx.Obs.TZOffsetSeconds)
			}
		})
	}
}

func TestWeatherStaysFreshUntilProviderExpiry(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	cfg := WeatherConfig{Enabled: true}
	obs := weatherObservation{FetchedAt: now.Add(-35 * time.Minute)}
	if newWeatherState(cfg, obs, true, airObservation{}, false, now).Fresh {
		t.Fatal("35-min-old observation without expiry is fresh")
	}
	obs.ValidUntil = now.Add(-5 * time.Minute)
	if !newWeatherState(cfg, obs, true, airObservation{}, false, now).Fresh {
		t.Fatal("observation the provider called valid 5 min ago is stale")
	}
}
