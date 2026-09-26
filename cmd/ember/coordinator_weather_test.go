package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestReconcileWeatherTilePushesAndClears(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RotateInApps = boolPtr(true)
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()

	// A fresh observation pushes the single ember-weather tile.
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherRain, TempC: 12, FetchedAt: now}
	app.weather.have = true
	app.weather.mu.Unlock()
	c.reconcileTiles(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-weather" {
		t.Fatalf("expected one ember-weather push, got %v", names)
	}

	// Unchanged within the refresh interval → no re-push.
	c.reconcileTiles(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("unchanged tile re-pushed: %d, want 1", got)
	}

	// Stale observation → the tile is cleared.
	c.reconcileTiles(now.Add(weatherTileStaleTTL + time.Minute))
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-weather" {
		t.Errorf("stale tile should be cleared, got %v", cleared)
	}
}

// TestWeatherTileCarriesOverlay: the conditions tile names the observation's
// NG overlay in every icon mode, and overlay:false leaves it off.
func TestWeatherTileCarriesOverlay(t *testing.T) {
	cases := []struct {
		name    string
		native  bool
		overlay *bool
		want    any
	}{
		{"drawn", false, nil, render.OverlayThunder},
		{"native icon", true, nil, render.OverlayThunder},
		{"toggle off", false, boolPtr(false), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := &recordingPublisher{}
			cfg := defaultConfig()
			cfg.Weather.applyDefaults()
			cfg.Weather.Enabled = true
			cfg.Weather.TileNativeIcons = tc.native
			if tc.overlay != nil {
				cfg.Weather.Overlay = tc.overlay
			}
			app := NewApp(cfg, pub, testLogger())
			now := time.Now()
			app.weather.mu.Lock()
			app.weather.obs = weatherObservation{Condition: render.WeatherStorm, TempC: 18,
				Overlay: render.OverlayThunder, FetchedAt: now}
			app.weather.have = true
			app.weather.mu.Unlock()

			app.coord.reconcileTiles(now)

			pub.mu.Lock()
			defer pub.mu.Unlock()
			if len(pub.customApps) != 1 {
				t.Fatalf("want one push, got %d", len(pub.customApps))
			}
			if got := pub.customApps[0]["overlay"]; got != tc.want {
				t.Errorf("overlay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReconcileForecastTilePushesAndClears(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.ForecastTile = boolPtr(true)
	cfg.Weather.RotateInApps = boolPtr(false) // isolate the forecast tile
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()

	// Fresh observation WITH hourly data pushes the ember-forecast tile.
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{
		Condition: render.WeatherClear, TempC: 20, FetchedAt: now,
		Hourly: []float64{18, 19, 20, 21, 22},
	}
	app.weather.have = true
	app.weather.mu.Unlock()
	c.reconcileTiles(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-forecast" {
		t.Fatalf("expected one ember-forecast push, got %v", names)
	}

	// Unchanged within the refresh interval → no re-push.
	c.reconcileTiles(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("unchanged forecast tile re-pushed: %d, want 1", got)
	}

	// Stale observation → cleared.
	c.reconcileTiles(now.Add(weatherTileStaleTTL + time.Minute))
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-forecast" {
		t.Errorf("stale forecast tile should be cleared, got %v", cleared)
	}
}

func TestReconcileAirTilePushesAndClears(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.AirTile = boolPtr(true)
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()

	// A fresh air observation pushes the single ember-air tile.
	app.weather.mu.Lock()
	app.weather.air = airObservation{AQI: 42, HourlyAQI: []float64{40, 45, 50}, FetchedAt: now}
	app.weather.haveAir = true
	app.weather.mu.Unlock()
	c.reconcileTiles(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-air" {
		t.Fatalf("expected one ember-air push, got %v", names)
	}

	// Unchanged within the refresh interval → no re-push.
	c.reconcileTiles(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("unchanged air tile re-pushed: %d, want 1", got)
	}

	// Stale observation → cleared.
	c.reconcileTiles(now.Add(weatherTileStaleTTL + time.Minute))
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-air" {
		t.Errorf("stale air tile should be cleared, got %v", cleared)
	}
}

func TestReconcileAirTileDisabled(t *testing.T) {
	now := time.Now()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.AirTile = boolPtr(false)
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.air = airObservation{AQI: 42, HourlyAQI: []float64{40}, FetchedAt: now}
	app.weather.haveAir = true
	app.weather.mu.Unlock()
	app.coord.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("air-off should push nothing, got %d", got)
	}
}

// After a server restart the in-memory push trackers start empty, but ember-
// managed custom apps from the previous run are still on the device. Adopting
// the device's app loop must let the reconcilers clear the ones no longer
// wanted (here: weather disabled), without touching the base/native apps.
func TestAdoptClearsStaleManagedAppsAfterRestart(t *testing.T) {
	pub := &recordingPublisher{loopApps: []string{
		"Time", "ember", "ember-weather", "ember-forecast", "ember-air", "ember-usage-claude-5h",
	}}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = false // weather off → its tiles should be cleared
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()

	if !c.adoptDeviceManagedApps() {
		t.Fatal("adopt should succeed when the device loop is readable")
	}
	c.reconcileTiles(now)

	cleared := pub.ClearedAppsSnapshot()
	for _, want := range []string{"ember-weather", "ember-forecast", "ember-air", "ember-usage-claude-5h"} {
		found := false
		for _, c := range cleared {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q to be cleared after adopt, got %v", want, cleared)
		}
	}
	for _, keep := range []string{"ember", "Time"} {
		for _, c := range cleared {
			if c == keep {
				t.Errorf("base/native app %q must never be cleared, got %v", keep, cleared)
			}
		}
	}
}

func TestReconcileForecastTileDisabledOrNoHourly(t *testing.T) {
	now := time.Now()
	// Forecast tile turned off → nothing pushed even with hourly data.
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.ForecastTile = boolPtr(false)
	cfg.Weather.RotateInApps = boolPtr(false) // isolate the forecast tile
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: now, Hourly: []float64{1, 2, 3}}
	app.weather.have = true
	app.weather.mu.Unlock()
	app.coord.reconcileTiles(now)
	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("forecast-off should push nothing, got %d", got)
	}

	// Enabled but no hourly data → nothing pushed (tile needs data).
	pub2 := &recordingPublisher{}
	cfg2 := defaultConfig()
	cfg2.Weather.applyDefaults()
	cfg2.Weather.Enabled = true
	cfg2.Weather.ForecastTile = boolPtr(true)
	cfg2.Weather.RotateInApps = boolPtr(false) // isolate the forecast tile
	app2 := NewApp(cfg2, pub2, testLogger())
	app2.weather.mu.Lock()
	app2.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: now} // Hourly nil
	app2.weather.have = true
	app2.weather.mu.Unlock()
	app2.coord.reconcileTiles(now)
	if got := len(pub2.CustomNamesSnapshot()); got != 0 {
		t.Errorf("no-hourly should push nothing, got %d", got)
	}
}

func TestForecastDefaultsAndWindow(t *testing.T) {
	var c WeatherConfig
	c.applyDefaults()
	if !c.ForecastTileEnabled() {
		t.Error("ForecastTile should default on")
	}
	if c.ForecastHours != 24 {
		t.Errorf("ForecastHours default = %d, want 24", c.ForecastHours)
	}
	// Out-of-range hours clamp.
	c2 := WeatherConfig{ForecastHours: 3}
	c2.applyDefaults()
	if c2.ForecastHours != 6 {
		t.Errorf("ForecastHours 3 clamped to %d, want 6", c2.ForecastHours)
	}
	c3 := WeatherConfig{ForecastHours: 99}
	c3.applyDefaults()
	if c3.ForecastHours != 24 {
		t.Errorf("ForecastHours 99 clamped to %d, want 24", c3.ForecastHours)
	}
	// forecastWindow slices to the hours and is nil-safe.
	if got := forecastWindow([]float64{1, 2, 3, 4, 5}, 3); len(got) != 3 {
		t.Errorf("window len = %d, want 3", len(got))
	}
	if got := forecastWindow(nil, 12); got != nil {
		t.Errorf("nil window should stay nil, got %v", got)
	}
}

func TestReconcileWeatherTileDisabledOrNoRotate(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RotateInApps = boolPtr(false) // tile suppressed even with fresh data
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: time.Now()}
	app.weather.have = true
	app.weather.mu.Unlock()
	app.coord.reconcileTiles(time.Now())
	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("rotate-off should push nothing, got %d", got)
	}
}

func TestReconcileTilesNativeIcons(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.TileNativeIcons = true
	app := NewApp(cfg, pub, testLogger())
	now := time.Now()
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherRain, TempC: 12,
		Hourly: []float64{10, 11, 12}, FetchedAt: now}
	app.weather.have = true
	app.weather.mu.Unlock()

	app.coord.reconcileTiles(now)
	app.coord.reconcileTiles(now)
	apps := pub.CustomAppsSnapshot()
	if len(apps) != 2 {
		t.Fatalf("expected weather+forecast pushes, got %d", len(apps))
	}
	// Conditions tile carries the native icon; digits stay drawn.
	if apps[0]["icon"] != "72" { // rain default gallery ID
		t.Errorf("weather payload icon = %v, want 72", apps[0]["icon"])
	}
	if _, has := apps[0]["text"]; has {
		t.Errorf("weather payload carries native text; digits must stay drawn")
	}
	// The forecast tile is full-width bars — no icon slot, native mode is moot.
	if _, has := apps[1]["icon"]; has {
		t.Errorf("forecast payload must not carry an icon (bars own the matrix)")
	}
}
