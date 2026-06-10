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
	cfg.Weather.RotateInApps = true
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()

	// A fresh observation pushes the single ember-weather tile.
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherRain, TempC: 12, FetchedAt: now}
	app.weather.have = true
	app.weather.mu.Unlock()
	c.reconcileWeatherApp(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-weather" {
		t.Fatalf("expected one ember-weather push, got %v", names)
	}

	// Unchanged within the refresh interval → no re-push.
	c.reconcileWeatherApp(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("unchanged tile re-pushed: %d, want 1", got)
	}

	// Stale observation → the tile is cleared.
	c.reconcileWeatherApp(now.Add(weatherTileStaleTTL + time.Minute))
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-weather" {
		t.Errorf("stale tile should be cleared, got %v", cleared)
	}
}

func TestReconcileForecastTilePushesAndClears(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.ForecastTile = true
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
	c.reconcileForecastApp(now)
	if names := pub.CustomNamesSnapshot(); len(names) != 1 || names[0] != "ember-forecast" {
		t.Fatalf("expected one ember-forecast push, got %v", names)
	}

	// Unchanged within the refresh interval → no re-push.
	c.reconcileForecastApp(now.Add(time.Minute))
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("unchanged forecast tile re-pushed: %d, want 1", got)
	}

	// Stale observation → cleared.
	c.reconcileForecastApp(now.Add(weatherTileStaleTTL + time.Minute))
	if cleared := pub.ClearedAppsSnapshot(); len(cleared) != 1 || cleared[0] != "ember-forecast" {
		t.Errorf("stale forecast tile should be cleared, got %v", cleared)
	}
}

// After a server restart the in-memory push trackers start empty, but ember-
// managed custom apps from the previous run are still on the device. Adopting
// the device's app loop must let the reconcilers clear the ones no longer
// wanted (here: weather disabled), without touching the base/native apps.
func TestAdoptClearsStaleManagedAppsAfterRestart(t *testing.T) {
	pub := &recordingPublisher{loopApps: []string{
		"Time", "ember", "ember-weather", "ember-forecast", "ember-usage-claude-5h",
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
	c.reconcileWeatherApp(now)
	c.reconcileForecastApp(now)
	c.reconcileUsageApps(now, Snapshot{})

	cleared := pub.ClearedAppsSnapshot()
	for _, want := range []string{"ember-weather", "ember-forecast", "ember-usage-claude-5h"} {
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
	cfg.Weather.ForecastTile = false
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: now, Hourly: []float64{1, 2, 3}}
	app.weather.have = true
	app.weather.mu.Unlock()
	app.coord.reconcileForecastApp(now)
	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("forecast-off should push nothing, got %d", got)
	}

	// Enabled but no hourly data → nothing pushed (tile needs data).
	pub2 := &recordingPublisher{}
	cfg2 := defaultConfig()
	cfg2.Weather.applyDefaults()
	cfg2.Weather.Enabled = true
	cfg2.Weather.ForecastTile = true
	app2 := NewApp(cfg2, pub2, testLogger())
	app2.weather.mu.Lock()
	app2.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: now} // Hourly nil
	app2.weather.have = true
	app2.weather.mu.Unlock()
	app2.coord.reconcileForecastApp(now)
	if got := len(pub2.CustomNamesSnapshot()); got != 0 {
		t.Errorf("no-hourly should push nothing, got %d", got)
	}
}

func TestForecastDefaultsAndWindow(t *testing.T) {
	var c WeatherConfig
	c.applyDefaults()
	if !c.ForecastTile {
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
	cfg.Weather.RotateInApps = false // tile suppressed even with fresh data
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherClear, TempC: 20, FetchedAt: time.Now()}
	app.weather.have = true
	app.weather.mu.Unlock()
	app.coord.reconcileWeatherApp(time.Now())
	if got := len(pub.CustomNamesSnapshot()); got != 0 {
		t.Errorf("rotate-off should push nothing, got %d", got)
	}
}
