package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type providerStubs struct {
	omHits  atomic.Int32
	metHits atomic.Int32
	omReq   chan struct{}
	metReq  chan struct{}
	onOM    func()
	metFail atomic.Bool
	metWait atomic.Int32
}

func signalStub(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func newProviderSwitchApp(t *testing.T) (*App, *providerStubs) {
	t.Helper()
	st := &providerStubs{omReq: make(chan struct{}, 1), metReq: make(chan struct{}, 1)}
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.omHits.Add(1)
		if st.onOM != nil {
			st.onOM()
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":21,"weather_code":61,"time":1790409600}}`))
		signalStub(st.omReq)
	}))
	t.Cleanup(om.Close)
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.metHits.Add(1)
		if st.metFail.Load() {
			code := http.StatusServiceUnavailable
			if secs := st.metWait.Load(); secs > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(secs)))
				code = http.StatusTooManyRequests
			}
			http.Error(w, "down", code)
			return
		}
		_, _ = w.Write([]byte(`{"properties":{"timeseries":[{"time":"2026-09-26T07:00:00Z","data":{"instant":{"details":{"air_temperature":4}},"next_1_hours":{"summary":{"symbol_code":"snow"}}}}]}}`))
		signalStub(st.metReq)
	}))
	t.Cleanup(met.Close)
	air := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected air-quality request %s", r.URL)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	t.Cleanup(air.Close)

	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Latitude, cfg.Weather.Longitude = 59.9, 10.7
	cfg.Weather.RefreshMinutes = 10
	cfg.Weather.PopupOnChange = boolPtr(false)
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.SunPopups = boolPtr(false)
	cfg.Weather.AirTile = boolPtr(false)
	cfg.Weather.AirPopupThreshold = 0
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	app.weatherFetcher = newWeatherFetcher()
	app.weatherFetcher.openMeteoBase = om.URL
	app.weatherFetcher.metNoBase = met.URL
	app.weatherFetcher.airQualityBase = air.URL
	return app, st
}

func putWeatherProvider(t *testing.T, app *App, provider string) {
	t.Helper()
	w := httptest.NewRecorder()
	app.handleWeatherConfigPut(w, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(`{"provider":"`+provider+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT provider %s = %d %s", provider, w.Code, w.Body.String())
	}
}

func TestWeatherProviderPutHidesOldObservation(t *testing.T) {
	app, _ := newProviderSwitchApp(t)
	now := time.Now()
	app.pollWeather(context.Background(), now)
	if kw := app.knobWeather(now); kw == nil || kw.Code != "61" || kw.Provider != "open-meteo" {
		t.Fatalf("knob weather before switch = %+v", kw)
	}

	putWeatherProvider(t, app, "met-no")

	if kw := app.knobWeather(now); kw != nil {
		t.Errorf("knob view pairs %q with old data: %+v", kw.Provider, kw)
	}
	if out := app.buildWeatherState(now); out.Current != nil {
		t.Errorf("weather state pairs %q with old data: %+v", out.Provider, out.Current)
	}
	if wx := app.weather.state(app.cfg.Load().Weather, now); wx.HaveObs || wx.live() {
		t.Errorf("clock state still has the old observation: %+v", wx.Obs)
	}
}

func TestWeatherProviderSwitchDiscardsInFlightFetch(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	st.onOM = func() {
		app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
	}
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)

	app.weather.mu.RLock()
	have, last := app.weather.have, app.weather.lastFetch
	app.weather.mu.RUnlock()
	if have || !last.IsZero() {
		t.Fatalf("old provider's late fetch landed: have=%v lastFetch=%v", have, last)
	}

	app.pollWeather(context.Background(), t0.Add(time.Second))
	if st.metHits.Load() != 1 {
		t.Fatalf("met-no fetched %d times after the switch, want 1", st.metHits.Load())
	}
	kw := app.knobWeather(t0.Add(time.Second))
	if kw == nil || kw.Provider != "met-no" || kw.Code != "snow" {
		t.Fatalf("knob weather after refetch = %+v", kw)
	}
}

func TestWeatherProviderSwitchBackDuringFetchKeepsResult(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	st.onOM = func() {
		app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
		app.updateConfig(func(c *Config) { c.Weather.Provider = "open-meteo" })
	}
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)

	app.weather.mu.RLock()
	provider, have, last := app.weather.source.provider, app.weather.have, app.weather.lastFetch
	app.weather.mu.RUnlock()
	if provider != "open-meteo" || !have || !last.Equal(t0) {
		t.Fatalf("store after A->B->A = provider %q have=%v lastFetch=%v, want open-meteo, true, %v", provider, have, last, t0)
	}
	if kw := app.knobWeather(t0); kw == nil || kw.Provider != "open-meteo" || kw.Code != "61" {
		t.Fatalf("knob weather after A->B->A = %+v", kw)
	}
	if st.metHits.Load() != 0 {
		t.Errorf("met-no fetched %d times, want 0", st.metHits.Load())
	}
}

func waitWeatherCommit(t *testing.T, app *App, seq uint64, what string) {
	t.Helper()
	deadline := time.After(chanWaitBound)
	for {
		_, changed := app.changes.subscribe()
		if app.changes.since(seq)&topicWeather != 0 {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("%s: no weather commit within %v", what, chanWaitBound)
		}
	}
}

func TestStartWeatherRefetchesOnProviderChange(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	seq, _ := app.changes.subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.StartWeather(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	recvWithin(t, st.omReq, "first open-meteo request")
	waitWeatherCommit(t, app, seq, "open-meteo observation")
	if kw := app.knobWeather(time.Now()); kw == nil || kw.Provider != "open-meteo" {
		t.Fatalf("knob weather before switch = %+v", kw)
	}

	seq, _ = app.changes.subscribe()
	putWeatherProvider(t, app, "met-no")

	recvWithin(t, st.metReq, "met-no request")
	waitWeatherCommit(t, app, seq, "met-no observation")
	if kw := app.knobWeather(time.Now()); kw == nil || kw.Provider != "met-no" || kw.Code != "snow" {
		t.Fatalf("knob weather after switch = %+v", kw)
	}
	if st.omHits.Load() != 1 || st.metHits.Load() != 1 {
		t.Errorf("fetches open-meteo=%d met-no=%d, want 1 each", st.omHits.Load(), st.metHits.Load())
	}
}

func TestWeatherRetriesSoonAfterAFailedFirstFetchFromANewProvider(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)
	app.pollWeather(ctx, t0)
	putWeatherProvider(t, app, "met-no")
	st.metFail.Store(true)

	t1 := t0.Add(time.Minute)
	app.pollWeather(ctx, t1)
	app.pollWeather(ctx, t1.Add(weatherRetryBase-time.Second))
	if n := st.metHits.Load(); n != 1 {
		t.Fatalf("met-no fetched %d times before the retry delay, want 1", n)
	}

	st.metFail.Store(false)
	t2 := t1.Add(weatherRetryBase)
	app.pollWeather(ctx, t2)
	if n := st.metHits.Load(); n != 2 {
		t.Fatalf("met-no fetched %d times %v after the failed first fetch, want a retry", n, weatherRetryBase)
	}
	if kw := app.knobWeather(t2); kw == nil || kw.Provider != "met-no" {
		t.Fatalf("knob weather after the retry = %+v", kw)
	}

	st.metFail.Store(true)
	t3 := t2.Add(10 * time.Minute)
	app.pollWeather(ctx, t3)
	app.pollWeather(ctx, t3.Add(weatherRetryBase))
	if n := st.metHits.Load(); n != 3 {
		t.Errorf("met-no fetched %d times; with an observation to show, a failure should wait the full refresh", n)
	}
}

func metHitMinutes(app *App, st *providerStubs, from time.Time, minutes int) []int {
	var at []int
	for m := 0; m <= minutes; m++ {
		n := st.metHits.Load()
		app.pollWeather(context.Background(), from.Add(time.Duration(m)*time.Minute))
		if st.metHits.Load() != n {
			at = append(at, m)
		}
	}
	return at
}

func TestWeatherDoublesTheRetryDelayWhileNothingIsShown(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	t0 := time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)
	app.pollWeather(context.Background(), t0)
	putWeatherProvider(t, app, "met-no")
	st.metFail.Store(true)

	t1 := t0.Add(time.Minute)
	if got, want := metHitMinutes(app, st, t1, 45), []int{0, 2, 6, 14, 24, 34, 44}; !slices.Equal(got, want) {
		t.Errorf("met-no fetched at minutes %v, want %v: 2, 4, 8 min, then refresh_minutes (10)", got, want)
	}

	t2 := t1.Add(50 * time.Minute)
	putWeatherProvider(t, app, "open-meteo")
	app.pollWeather(context.Background(), t2)
	putWeatherProvider(t, app, "met-no")
	if got, want := metHitMinutes(app, st, t2.Add(time.Minute), 7), []int{0, 2, 6}; !slices.Equal(got, want) {
		t.Errorf("after a provider switch met-no fetched at minutes %v, want %v: the delay starts over at 2 min", got, want)
	}
}

func TestWeatherHonoursRetryAfterOnAFailedFetch(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	t0 := time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)
	app.pollWeather(context.Background(), t0)
	putWeatherProvider(t, app, "met-no")
	st.metFail.Store(true)
	st.metWait.Store(15 * 60)

	if got, want := metHitMinutes(app, st, t0.Add(time.Minute), 20), []int{0, 15}; !slices.Equal(got, want) {
		t.Errorf("met-no fetched at minutes %v, want %v: nothing before the 429's Retry-After of 15 min", got, want)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"120", 2 * time.Minute},
		{"-5", 0},
		{"soon", 0},
		{now.Add(10 * time.Minute).Format(http.TimeFormat), 10 * time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in, now); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
