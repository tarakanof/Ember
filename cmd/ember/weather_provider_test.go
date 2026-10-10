package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type providerStubs struct {
	omHits  atomic.Int32
	metHits atomic.Int32
	onOM    func()
}

func newProviderSwitchApp(t *testing.T) (*App, *providerStubs) {
	t.Helper()
	st := &providerStubs{}
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.omHits.Add(1)
		if st.onOM != nil {
			st.onOM()
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":21,"weather_code":61,"time":1790409600}}`))
	}))
	t.Cleanup(om.Close)
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.metHits.Add(1)
		_, _ = w.Write([]byte(`{"properties":{"timeseries":[{"time":"2026-09-26T07:00:00Z","data":{"instant":{"details":{"air_temperature":4}},"next_1_hours":{"summary":{"symbol_code":"snow"}}}}]}}`))
	}))
	t.Cleanup(met.Close)

	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Latitude, cfg.Weather.Longitude = 59.9, 10.7
	cfg.Weather.RefreshMinutes = 10
	cfg.Weather.PopupOnChange = boolPtr(false)
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.SunPopups = boolPtr(false)
	cfg.Weather.AirTile = boolPtr(false)
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	app.weatherFetcher = newWeatherFetcher()
	app.weatherFetcher.openMeteoBase = om.URL
	app.weatherFetcher.metNoBase = met.URL
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

func TestStartWeatherRefetchesOnProviderChange(t *testing.T) {
	app, st := newProviderSwitchApp(t)
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

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("first open-meteo fetch", func() bool { return app.knobWeather(time.Now()) != nil })

	putWeatherProvider(t, app, "met-no")

	waitFor("met-no fetch", func() bool { return st.metHits.Load() == 1 })
	waitFor("met-no observation", func() bool {
		kw := app.knobWeather(time.Now())
		return kw != nil && kw.Code == "snow"
	})
	if st.omHits.Load() != 1 {
		t.Errorf("open-meteo fetched %d times, want 1", st.omHits.Load())
	}
}
