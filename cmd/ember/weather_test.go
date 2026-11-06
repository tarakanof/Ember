package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/weather"
)

func TestWeatherPopupCarriesOverlay(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	cfg := defaultConfig().Weather
	cfg.ApplyDefaults()
	obs := weather.Observation{Condition: render.WeatherSnow, TempC: -2, Overlay: render.OverlaySnow}

	app.sendWeatherPopup(context.Background(), obs, cfg, 30, "")
	cfg.Overlay = boolPtr(false)
	app.sendWeatherPopup(context.Background(), obs, cfg, 30, "")

	got := pub.NotifySnapshot()
	if len(got) != 2 {
		t.Fatalf("want 2 popups, got %d", len(got))
	}
	if got[0]["overlay"] != render.OverlaySnow {
		t.Errorf("overlay = %v, want %q", got[0]["overlay"], render.OverlaySnow)
	}
	if _, has := got[1]["overlay"]; has {
		t.Errorf("overlay:false must drop the key, got %v", got[1]["overlay"])
	}
}

func TestWeatherTempText(t *testing.T) {
	if got := weatherTempText(21.4, "metric"); got != "21°" {
		t.Errorf("metric = %q, want 21°", got)
	}
	if got := weatherTempText(0, "imperial"); got != "32°" {
		t.Errorf("imperial 0C = %q, want 32°", got)
	}
	if got := weatherTempText(-4.6, "metric"); got != "-5°" {
		t.Errorf("negative = %q, want -5°", got)
	}
}

func TestEvaluateWeatherPopupPriority(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	app := NewApp(cfg, pub, testLogger())
	wcfg := app.cfg.Load().Weather

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	storm := weather.Observation{Condition: render.WeatherStorm, TempC: 18, Severe: true, FetchedAt: now}
	if !app.evaluateWeatherPopup(context.Background(), now, storm, render.WeatherClouds, false, time.Time{}, wcfg) {
		t.Fatal("severe transition should fire")
	}
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Errorf("severe transition should send one popup: %+v", pub.notify)
	}
	if pub.notify[0]["soundRtttl"] != defaultWeatherSevereSound {
		t.Errorf("severe popup soundRtttl = %v, want %q", pub.notify[0]["soundRtttl"], defaultWeatherSevereSound)
	}
	if pub.notify[0]["name"] != notifyNameWeatherPopup {
		t.Errorf("name = %v, want %q", pub.notify[0]["name"], notifyNameWeatherPopup)
	}
	if len(pub.rtttls) != 0 {
		t.Errorf("the severe chime rides on the notification, got %v out-of-band", pub.rtttls)
	}
	pub.mu.Unlock()

	rain := weather.Observation{Condition: render.WeatherRain, TempC: 16, Severe: false, FetchedAt: now}
	if !app.evaluateWeatherPopup(context.Background(), now, rain, render.WeatherStorm, true, now, wcfg) {
		t.Fatal("condition change should fire")
	}
	pub.mu.Lock()
	last := pub.notify[len(pub.notify)-1]
	if last["sound"] != nil || last["soundRtttl"] != nil {
		t.Errorf("change popup should be silent: %+v", last)
	}
	if len(pub.rtttls) != 0 {
		t.Errorf("a non-severe change popup must not chime: %v", pub.rtttls)
	}
	pub.mu.Unlock()

	if app.evaluateWeatherPopup(context.Background(), now, rain, render.WeatherRain, false, now, wcfg) {
		t.Error("stable weather within interval should not fire")
	}
}

func TestApplyWeatherSettingsValidation(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	ownIconJobs(t, app)
	bad := weather.Config{Provider: "open-meteo", Units: "metric", Latitude: 200, Longitude: 0, RefreshMinutes: 10, PopupDurationSeconds: 30}
	if err := putWeather(app, bad); err == nil {
		t.Error("latitude 200 should be rejected")
	}
	good := weather.Config{Provider: "met-no", Units: "imperial", Latitude: 52, Longitude: 4, RefreshMinutes: 15, PopupDurationSeconds: 20}
	if err := putWeather(app, good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if app.cfg.Load().Weather.Provider != "met-no" {
		t.Error("config not swapped into live cfg")
	}
}

func TestApplyWeatherSettingsPreservesDisables(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	ownIconJobs(t, app)
	off := weather.Config{
		Enabled: true, Provider: "open-meteo", Units: "metric", Latitude: 52, Longitude: 4,
		RefreshMinutes: 10, PopupDurationSeconds: 30,
		RotateInApps: boolPtr(false), PopupOnChange: boolPtr(false), SevereAlert: boolPtr(false), PopupIntervalMinutes: intPtr(0),
	}
	if err := putWeather(app, off); err != nil {
		t.Fatalf("config rejected: %v", err)
	}
	got := app.cfg.Load().Weather
	if got.RotateInAppsEnabled() || got.PopupOnChangeEnabled() || got.SevereAlertEnabled() || got.PopupIntervalMins() != 0 {
		t.Errorf("disables were clobbered: rotate=%v onChange=%v severe=%v interval=%d",
			got.RotateInAppsEnabled(), got.PopupOnChangeEnabled(), got.SevereAlertEnabled(), got.PopupIntervalMins())
	}
}

func TestWeatherAirValidation(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	ownIconJobs(t, app)
	bad := weather.Config{Provider: "open-meteo", Units: "metric", RefreshMinutes: 10, PopupDurationSeconds: 30, AirPopupThreshold: 201}
	if err := putWeather(app, bad); err == nil {
		t.Error("air_popup_threshold 201 should be rejected")
	}
	bad.AirPopupThreshold = -1
	if err := putWeather(app, bad); err == nil {
		t.Error("air_popup_threshold -1 should be rejected")
	}
	off := weather.Config{Provider: "open-meteo", Units: "metric", RefreshMinutes: 10, PopupDurationSeconds: 30, AirTile: boolPtr(false), AirPopupThreshold: 0}
	if err := putWeather(app, off); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	got := app.cfg.Load().Weather
	if got.AirTileEnabled() || got.AirPopupThreshold != 0 {
		t.Errorf("air disables clobbered: tile=%v threshold=%d", got.AirTileEnabled(), got.AirPopupThreshold)
	}
}

func newAirTestApp(t *testing.T, pub *recordingPublisher, aqi *float64, hits *int32) *App {
	t.Helper()
	weatherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0}}`))
	}))
	t.Cleanup(weatherSrv.Close)
	airSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		fmt.Fprintf(w, `{"current":{"european_aqi":%g},"hourly":{"european_aqi":[%g]}}`, *aqi, *aqi)
	}))
	t.Cleanup(airSrv.Close)

	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RefreshMinutes = 10
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.AirPopupThreshold = 80
	app := NewApp(cfg, pub, testLogger())
	app.weatherFetcher = weather.NewFetcher()
	app.weatherFetcher.OpenMeteoBase = weatherSrv.URL
	app.weatherFetcher.AirQualityBase = airSrv.URL
	return app
}

func TestPollAirStoresAndPopsOnEdge(t *testing.T) {
	pub := &recordingPublisher{}
	aqi := 50.0
	var hits int32
	app := newAirTestApp(t, pub, &aqi, &hits)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	countAirPopups := func() int {
		pub.mu.Lock()
		defer pub.mu.Unlock()
		n := 0
		for _, p := range pub.notify {
			if s, _ := p["text"].(string); strings.HasPrefix(s, "AIR ") {
				n++
			}
		}
		return n
	}

	app.pollWeather(context.Background(), t0)
	wx := app.weather.state(app.cfg.Load().Weather, t0)
	air, have := wx.Air, wx.HaveAir
	if !have || air.AQI != 50 || len(air.HourlyAQI) != 1 {
		t.Fatalf("air obs not stored: %+v have=%v", air, have)
	}
	if countAirPopups() != 0 {
		t.Fatal("below-threshold reading must not pop")
	}

	aqi = 85
	app.pollWeather(context.Background(), t0.Add(11*time.Minute))
	if countAirPopups() != 1 {
		t.Fatalf("rising edge should fire exactly one popup, got %d", countAirPopups())
	}

	aqi = 90
	app.pollWeather(context.Background(), t0.Add(22*time.Minute))
	if countAirPopups() != 1 {
		t.Fatalf("staying above threshold must not re-fire, got %d", countAirPopups())
	}

	aqi = 70
	app.pollWeather(context.Background(), t0.Add(33*time.Minute))
	aqi = 81
	app.pollWeather(context.Background(), t0.Add(44*time.Minute))
	if countAirPopups() != 2 {
		t.Fatalf("re-armed crossing should fire a second popup, got %d", countAirPopups())
	}
}

func TestPollAirFirstObservationAboveThresholdPops(t *testing.T) {
	pub := &recordingPublisher{}
	aqi := 120.0
	var hits int32
	app := newAirTestApp(t, pub, &aqi, &hits)
	app.pollWeather(context.Background(), time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	pub.mu.Lock()
	defer pub.mu.Unlock()
	found := false
	for _, p := range pub.notify {
		if p["text"] == "AIR EXTREME 120" {
			found = true
		}
	}
	if !found {
		t.Errorf("first above-threshold observation should pop, notify=%v", pub.notify)
	}
}

func TestPollAirGatedByConfig(t *testing.T) {
	pub := &recordingPublisher{}
	aqi := 50.0
	var hits int32
	app := newAirTestApp(t, pub, &aqi, &hits)
	cur := *app.cfg.Load()
	cur.Weather.AirTile = boolPtr(false)
	cur.Weather.AirPopupThreshold = 0
	app.cfg.Store(&cur)
	app.pollWeather(context.Background(), time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("AQ fetched %d times with tile+popup both off, want 0", got)
	}
}

func TestPollAirFailureKeepsWeatherWorking(t *testing.T) {
	pub := &recordingPublisher{}
	weatherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0}}`))
	}))
	defer weatherSrv.Close()
	airSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer airSrv.Close()

	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	app := NewApp(cfg, pub, testLogger())
	app.weatherFetcher = weather.NewFetcher()
	app.weatherFetcher.OpenMeteoBase = weatherSrv.URL
	app.weatherFetcher.AirQualityBase = airSrv.URL

	app.pollWeather(context.Background(), time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	wx := app.weather.state(app.cfg.Load().Weather, time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	if !wx.HaveObs {
		t.Error("a failing AQ fetch must not block the weather observation")
	}
	if wx.HaveAir {
		t.Error("failed AQ fetch must not record an observation")
	}
}

func TestPollWeatherBackoffAndSeed(t *testing.T) {
	var hits int32
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0}}`))
	}))
	defer srv.Close()

	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RefreshMinutes = 10
	cfg.Weather.PopupIntervalMinutes = intPtr(120)
	app := NewApp(cfg, pub, testLogger())
	app.weatherFetcher = weather.NewFetcher()
	app.weatherFetcher.OpenMeteoBase = srv.URL
	app.weatherFetcher.AirQualityBase = notFoundServer(t)

	t0 := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	app.pollWeather(context.Background(), t0)
	app.pollWeather(context.Background(), t0.Add(time.Minute))
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("failing provider was refetched within the interval: %d hits, want 1", got)
	}

	fail = false
	app.pollWeather(context.Background(), t0.Add(11*time.Minute))
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("recovered fetch didn't happen: %d hits, want 2", got)
	}
	pub.mu.Lock()
	popups := len(pub.notify)
	pub.mu.Unlock()
	if popups != 0 {
		t.Errorf("first successful observation fired %d popups, want 0 (interval clock should seed silently)", popups)
	}

	app.pollWeather(context.Background(), t0.Add(11*time.Minute+121*time.Minute))
	pub.mu.Lock()
	popups = len(pub.notify)
	pub.mu.Unlock()
	if popups != 1 {
		t.Errorf("interval popup didn't fire after a full interval: %d popups, want 1", popups)
	}
}

func validWeatherConfig() weather.Config {
	return weather.Config{
		Provider:             "open-meteo",
		Units:                "metric",
		RefreshMinutes:       10,
		PopupDurationSeconds: 30,
		AirPopupThreshold:    80,
	}
}

func TestValidateWeatherIconIDs(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"numeric ok", "123", false},
		{"path traversal rejected", "../dev", true},
		{"alpha suffix rejected", "12a", true},
		{"empty rejected", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := validWeatherConfig()
			cfg.IconIDs = map[string]string{"clear": c.id}
			err := validateWeather(cfg)
			if (err != nil) != c.wantErr {
				t.Errorf("validateWeather icon_ids[clear]=%q err=%v, wantErr=%v", c.id, err, c.wantErr)
			}
			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "clear") {
					t.Errorf("error should name the offending key %q, got %v", "clear", err)
				}
			}
		})
	}
}

func TestValidateWeatherOKWithoutIconIDs(t *testing.T) {
	if err := validateWeather(validWeatherConfig()); err != nil {
		t.Errorf("baseline valid config should pass validateWeather: %v", err)
	}
}

func putWeather(a *App, c weather.Config) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = a.settings.weather.put(b)
	return err
}
