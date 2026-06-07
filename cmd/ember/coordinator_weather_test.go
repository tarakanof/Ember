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
