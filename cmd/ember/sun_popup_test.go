package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSunPopupFiresOncePerDay(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.SunPopups = boolPtr(true)
	cfg.Weather.Latitude = 10
	cfg.Weather.Longitude = 10
	app := NewApp(cfg, pub, testLogger())
	wcfg := app.cfg.Load().Weather

	date := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	sunrise, _, ok := sunTimes(10, 10, date)
	if !ok {
		t.Fatal("expected sunrise")
	}

	// At the sunrise instant → fires once with a SUNRISE label.
	app.checkSunPopups(context.Background(), sunrise, wcfg)
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Fatalf("expected one sun popup, got %d", len(pub.notify))
	}
	if txt, _ := pub.notify[0]["text"].(string); !strings.HasPrefix(txt, "SUNRISE ") {
		t.Errorf("popup text = %q, want SUNRISE …", txt)
	}
	pub.mu.Unlock()

	// A minute later (still in grace) → no duplicate.
	app.checkSunPopups(context.Background(), sunrise.Add(time.Minute), wcfg)
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Errorf("sunrise fired twice in one day: %d", len(pub.notify))
	}
	pub.mu.Unlock()
}

func TestSunPopupSkippedWhenDisabledOrBeforeEvent(t *testing.T) {
	date := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	sunrise, _, _ := sunTimes(10, 10, date)

	// Disabled → nothing.
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.SunPopups = boolPtr(false)
	app := NewApp(cfg, pub, testLogger())
	app.checkSunPopups(context.Background(), sunrise, app.cfg.Load().Weather)
	pub.mu.Lock()
	got := len(pub.notify)
	pub.mu.Unlock()
	if got != 0 {
		t.Errorf("disabled sun popups fired %d", got)
	}

	// Enabled but well before the event → nothing.
	pub2 := &recordingPublisher{}
	cfg2 := defaultConfig()
	cfg2.Weather.applyDefaults()
	cfg2.Weather.Enabled = true
	cfg2.Weather.SunPopups = boolPtr(true)
	cfg2.Weather.Latitude = 10
	cfg2.Weather.Longitude = 10
	app2 := NewApp(cfg2, pub2, testLogger())
	app2.checkSunPopups(context.Background(), sunrise.Add(-time.Hour), app2.cfg.Load().Weather)
	pub2.mu.Lock()
	got = len(pub2.notify)
	pub2.mu.Unlock()
	if got != 0 {
		t.Errorf("pre-event fired %d, want 0", got)
	}
}
