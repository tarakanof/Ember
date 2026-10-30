package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/weather"
)

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

type metStub struct {
	mu            sync.Mutex
	hits          atomic.Int32
	ims           []string
	lats          []string
	lastModified  string
	expires       time.Time
	date          time.Time
	age           string
	notModified   bool
	airFail       atomic.Bool
	airHits       atomic.Int32
	airRetryAfter string
	fail          atomic.Bool
	onRequest     func()
}

func newMetCacheApp(t *testing.T, st *metStub, airOn bool) *App {
	t.Helper()
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		if st.onRequest != nil {
			st.onRequest()
		}
		if st.fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.ims = append(st.ims, r.Header.Get("If-Modified-Since"))
		st.lats = append(st.lats, r.URL.Query().Get("lat"))
		w.Header().Set("Last-Modified", st.lastModified)
		w.Header().Set("Expires", st.expires.UTC().Format(http.TimeFormat))
		if st.date.IsZero() {
			w.Header()["Date"] = nil
		} else {
			w.Header().Set("Date", st.date.UTC().Format(http.TimeFormat))
		}
		if st.age != "" {
			w.Header().Set("Age", st.age)
		}
		if st.notModified && r.Header.Get("If-Modified-Since") == st.lastModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	t.Cleanup(met.Close)
	air := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.airHits.Add(1)
		if st.airFail.Load() {
			if st.airRetryAfter != "" {
				w.Header().Set("Retry-After", st.airRetryAfter)
			}
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"utc_offset_seconds":10800,"current":{"european_aqi":18}}`))
	}))
	t.Cleanup(air.Close)
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
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
	app.weatherFetcher = weather.NewFetcher()
	app.weatherFetcher.MetNoBase = met.URL
	app.weatherFetcher.AirQualityBase = air.URL
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

func TestMetNoMeasuresExpiresFromTheResponseDate(t *testing.T) {
	for _, skew := range []time.Duration{2 * time.Hour, -time.Hour} {
		t0 := metT0
		st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", date: t0.Add(skew), expires: t0.Add(skew + 25*time.Minute), notModified: true}
		app := newMetCacheApp(t, st, false)
		ctx := context.Background()

		app.pollWeather(ctx, t0)
		app.weather.mu.RLock()
		notBefore, valid := app.weather.notBefore, app.weather.obs.ValidUntil
		app.weather.mu.RUnlock()
		want := t0.Add(25 * time.Minute)
		if !notBefore.Equal(want) || !valid.Equal(want) {
			t.Errorf("MET clock %v off: notBefore %v ValidUntil %v, want %v (Expires - Date after the poll)", skew, notBefore, valid, want)
		}
		app.pollWeather(ctx, t0.Add(10*time.Minute))
		if st.hits.Load() != 1 {
			t.Errorf("MET clock %v off: %d requests by 20:10, want 1 (skipped until 20:25)", skew, st.hits.Load())
		}

		st.mu.Lock()
		st.date, st.expires = t0.Add(skew+30*time.Minute), t0.Add(skew+70*time.Minute)
		st.mu.Unlock()
		t2 := t0.Add(30 * time.Minute)
		app.pollWeather(ctx, t2)
		if st.hits.Load() != 2 {
			t.Fatalf("MET clock %v off: %d requests by 20:30, want 2 (revalidated after 20:25)", skew, st.hits.Load())
		}
		if wx := app.weather.state(app.cfg.Load().Weather, t2); !wx.Obs.ValidUntil.Equal(t0.Add(70 * time.Minute)) {
			t.Errorf("MET clock %v off: after the 304 ValidUntil %v, want %v", skew, wx.Obs.ValidUntil, t0.Add(70*time.Minute))
		}
	}
}

func TestMetNoTakesTheResponseAgeOffItsFreshness(t *testing.T) {
	cases := []struct {
		age  string
		want time.Duration
	}{{"3000", 10 * time.Minute}, {"5400", 0}, {"junk", 60 * time.Minute}, {"99999999999999999999", 0}}
	for _, c := range cases {
		t0 := metT0
		st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", date: t0, expires: t0.Add(60 * time.Minute), age: c.age}
		app := newMetCacheApp(t, st, false)
		app.pollWeather(context.Background(), t0)
		app.weather.mu.RLock()
		notBefore, valid := app.weather.notBefore, app.weather.obs.ValidUntil
		app.weather.mu.RUnlock()
		if want := t0.Add(c.want); !notBefore.Equal(want) || !valid.Equal(want) {
			t.Errorf("Age %s: notBefore %v ValidUntil %v, want %v (Expires - Date - Age, floored at now)", c.age, notBefore, valid, want)
		}
	}
}

func TestMetNoCapsFreshnessFromAReplayedDate(t *testing.T) {
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", date: t0.AddDate(0, 0, -30), expires: t0.Add(25 * time.Minute)}
	app := newMetCacheApp(t, st, false)
	ctx := context.Background()
	app.pollWeather(ctx, t0)
	app.weather.mu.RLock()
	notBefore, valid := app.weather.notBefore, app.weather.obs.ValidUntil
	app.weather.mu.RUnlock()
	if want := t0.Add(time.Hour); !notBefore.Equal(want) || !valid.Equal(want) {
		t.Errorf("Date 30 days behind Expires: notBefore %v ValidUntil %v, want %v (capped at max(refresh, 1h))", notBefore, valid, want)
	}
	app.pollWeather(ctx, t0.Add(61*time.Minute))
	if st.hits.Load() != 2 {
		t.Errorf("%d requests an hour later, want 2 (the cap lets the next poll through)", st.hits.Load())
	}
}

func TestMetNoCapsAnAbsoluteExpiresWithoutDate(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Duration
		want    time.Duration
	}{{"30 days ahead", 30 * 24 * time.Hour, time.Hour}, {"25 min ahead", 25 * time.Minute, 25 * time.Minute}}
	for _, c := range cases {
		t0 := metT0
		st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(c.expires)}
		app := newMetCacheApp(t, st, false)
		app.pollWeather(context.Background(), t0)
		app.weather.mu.RLock()
		notBefore, valid := app.weather.notBefore, app.weather.obs.ValidUntil
		app.weather.mu.RUnlock()
		if want := t0.Add(c.want); !notBefore.Equal(want) || !valid.Equal(want) {
			t.Errorf("no Date, Expires %s: notBefore %v ValidUntil %v, want %v (capped at max(refresh, 1h))", c.name, notBefore, valid, want)
		}
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
	app.weatherFetcher.OpenMeteoBase = om.URL
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
	cfg := weather.Config{Enabled: true}
	obs := weather.Observation{FetchedAt: now.Add(-35 * time.Minute)}
	if newWeatherState(cfg, obs, true, weather.AirObservation{}, false, now).Fresh {
		t.Fatal("35-min-old observation without expiry is fresh")
	}
	obs.ValidUntil = now.Add(-5 * time.Minute)
	if !newWeatherState(cfg, obs, true, weather.AirObservation{}, false, now).Fresh {
		t.Fatal("observation the provider called valid 5 min ago is stale")
	}
}

func TestMetNoPopsTheConditionChangeOnceForTheEntryNow(t *testing.T) {
	cases := []struct {
		name        string
		expires     time.Duration
		notModified bool
		polls       []int
		hits        int32
	}{
		{"skipped until Expires", 65 * time.Minute, false, []int{5, 61, 64}, 1},
		{"answered 304", 25 * time.Minute, true, []int{0, 61, 71}, 3},
		{"full 200 whose series starts an hour back", -time.Minute, false, []int{0, 61, 71}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: metT0.Add(c.expires), notModified: c.notModified}
			app := newMetCacheApp(t, st, false)
			pub := app.publisher.(*recordingPublisher)
			app.updateConfig(func(cfg *Config) { cfg.Weather.PopupOnChange = boolPtr(true) })
			for _, m := range c.polls {
				app.pollWeather(context.Background(), metT0.Add(time.Duration(m)*time.Minute))
			}
			if n := st.hits.Load(); n != c.hits {
				t.Fatalf("setup: %d MET requests, want %d", n, c.hits)
			}
			pub.mu.Lock()
			defer pub.mu.Unlock()
			var texts []string
			for _, p := range pub.notify {
				if s, _ := p["text"].(string); s != "" {
					texts = append(texts, s)
				}
			}
			if want := []string{"SNOW 13°"}; !slices.Equal(texts, want) {
				t.Errorf("popups %q over polls %v min after 20:00, want %q: the 21:00 lightsleet entry, once", texts, c.polls, want)
			}
		})
	}
}

func TestAirWaitsRefreshMinutesWhileWeatherRetries(t *testing.T) {
	st := &metStub{}
	st.fail.Store(true)
	app := newMetCacheApp(t, st, true)
	for _, m := range []int{0, 2, 6} {
		app.pollWeather(context.Background(), metT0.Add(time.Duration(m)*time.Minute))
	}
	if w, a := st.hits.Load(), st.airHits.Load(); w != 3 || a != 1 {
		t.Fatalf("after 3 weather retries in 6 min: weather %d, air %d requests; want 3 and 1 (air succeeded at 0)", w, a)
	}
	app.pollWeather(context.Background(), metT0.Add(14*time.Minute))
	if a := st.airHits.Load(); a != 2 {
		t.Errorf("air requests at 14 min = %d, want 2: refetched once refresh_minutes old", a)
	}
}

func TestAirFetchesOnTheNextPollOnceTheTileIsEnabled(t *testing.T) {
	st := &metStub{}
	app := newMetCacheApp(t, st, false)
	app.pollWeather(context.Background(), metT0)
	if a := st.airHits.Load(); a != 0 {
		t.Fatalf("air requests with the tile off = %d, want 0", a)
	}
	st.airFail.Store(true)
	app.updateConfig(func(cfg *Config) { cfg.Weather.AirTile = boolPtr(true) })
	at := airPollMinutes(app, st, 1, 8)
	if want := []int{1, 3, 7}; !slices.Equal(at, want) {
		t.Errorf("air fetched at minutes %v, want %v: on the first poll after enabling, then backing off while it fails", at, want)
	}
	if w := st.hits.Load(); w != 1 {
		t.Errorf("weather requests = %d, want 1: enabling air doesn't make weather due", w)
	}
}

func airPollMinutes(app *App, st *metStub, from, to int) []int {
	var at []int
	for m := from; m <= to; m++ {
		n := st.airHits.Load()
		app.pollWeather(context.Background(), metT0.Add(time.Duration(m)*time.Minute))
		if st.airHits.Load() != n {
			at = append(at, m)
		}
	}
	return at
}

func TestAirBacksOffWhilePermanentlyFailing(t *testing.T) {
	st := &metStub{}
	st.airFail.Store(true)
	app := newMetCacheApp(t, st, true)
	at := airPollMinutes(app, st, 0, 59)
	if want := []int{0, 2, 6, 14, 24, 34, 44, 54}; !slices.Equal(at, want) {
		t.Fatalf("air fetched at minutes %v over an hour of failures, want %v: 2, 4, 8 min, then refresh_minutes", at, want)
	}
	st.airFail.Store(false)
	app.pollWeather(context.Background(), metT0.Add(64*time.Minute))
	st.airFail.Store(true)
	if at := airPollMinutes(app, st, 65, 80); !slices.Equal(at, []int{74, 76, 80}) {
		t.Errorf("air fetched at minutes %v after a success at 64, want [74 76 80]: the success resets the backoff", at)
	}
}

func TestAirHonoursRetryAfterCappedAtRefreshMinutes(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
		want       []int
	}{
		{"5 min, a floor under the backoff", "300", []int{0, 5, 10, 18, 28}},
		{"an hour, capped at refresh_minutes", "3600", []int{0, 10, 20}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &metStub{airRetryAfter: c.retryAfter}
			st.airFail.Store(true)
			app := newMetCacheApp(t, st, true)
			if at := airPollMinutes(app, st, 0, 29); !slices.Equal(at, c.want) {
				t.Errorf("air fetched at minutes %v with Retry-After %s, want %v", at, c.retryAfter, c.want)
			}
		})
	}
}

func TestAirLocationSwitchClearsTheFailureGate(t *testing.T) {
	st := &metStub{}
	st.airFail.Store(true)
	app := newMetCacheApp(t, st, true)
	if at := airPollMinutes(app, st, 0, 6); !slices.Equal(at, []int{0, 2, 6}) {
		t.Fatalf("setup: air fetched at minutes %v, want [0 2 6]", at)
	}
	app.updateConfig(func(c *Config) { c.Weather.Latitude, c.Weather.Longitude = 40.7, -74 })
	if at := airPollMinutes(app, st, 7, 10); !slices.Equal(at, []int{7, 9}) {
		t.Errorf("air fetched at minutes %v after a location switch at 7, want [7 9]: the new location starts with no failures", at)
	}
}

func TestAirRetriesEveryMinuteAtRefreshMinutesOne(t *testing.T) {
	st := &metStub{}
	st.airFail.Store(true)
	app := newMetCacheApp(t, st, true)
	app.updateConfig(func(c *Config) { c.Weather.RefreshMinutes = 1 })
	if at := airPollMinutes(app, st, 0, 4); !slices.Equal(at, []int{0, 1, 2, 3, 4}) {
		t.Errorf("air fetched at minutes %v with refresh_minutes 1, want every minute: a failure never waits longer than a success", at)
	}
}
