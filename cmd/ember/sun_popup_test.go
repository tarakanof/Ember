package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

func TestSunPopupFiresOncePerDay(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.SunPopups = boolPtr(true)
	cfg.Weather.Latitude = 10
	cfg.Weather.Longitude = 10
	app := NewApp(cfg, pub, testLogger())
	wcfg := app.cfg.Load().Weather

	date := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	sunrise, _, ok := weather.SunTimes(10, 10, date).Pair()
	if !ok {
		t.Fatal("expected sunrise")
	}

	app.checkSunPopups(context.Background(), sunrise, wcfg)
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Fatalf("expected one sun popup, got %d", len(pub.notify))
	}
	if txt, _ := pub.notify[0]["text"].(string); !strings.HasPrefix(txt, "SUNRISE ") {
		t.Errorf("popup text = %q, want SUNRISE …", txt)
	}
	pub.mu.Unlock()

	app.checkSunPopups(context.Background(), sunrise.Add(time.Minute), wcfg)
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Errorf("sunrise fired twice in one day: %d", len(pub.notify))
	}
	pub.mu.Unlock()
}

func TestSunPopupSkippedWhenDisabledOrBeforeEvent(t *testing.T) {
	date := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	sunrise, _, _ := weather.SunTimes(10, 10, date).Pair()

	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
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

	pub2 := &recordingPublisher{}
	cfg2 := defaultConfig()
	cfg2.Weather.ApplyDefaults()
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

func TestSunPopupsFireOnceForEveryModelCrossingAllYear(t *testing.T) {
	t.Parallel()
	sites := []struct {
		name, zone string
		lat, lon   float64
	}{
		{"Tromsø", "Europe/Oslo", 69.65, 18.96},
		{"Utqiaġvik", "America/Anchorage", 71.29, -156.79},
		{"McMurdo", "Antarctica/McMurdo", -77.85, 166.67},
		{"Reykjavík", "Atlantic/Reykjavik", 64.15, -21.94},
		{"Longyearbyen", "Arctic/Longyearbyen", 78.22, 15.65},
	}
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range sites {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(s.zone)
			if err != nil {
				t.Fatal(err)
			}
			var log sunPopupLog
			var fired []sunCrossingAt
			evs := sunEventsByDate{lat: s.lat, lon: s.lon}
			for now := from; now.Before(to); now = now.Add(time.Minute) {
				_, off := now.In(loc).Zone()
				obs := weather.Observation{TZKnown: true, TZOffsetSeconds: off}
				for _, ev := range log.due(evs.at(now), now) {
					fired = append(fired, sunCrossingAt{ev.At, ev.Rise})
					if got, want := weather.SunClock(ev.At, obs, s.lon), ev.At.In(loc).Format("15:04"); got != want {
						t.Errorf("%s %v: label %s, local time %s", crossingWord(ev.Rise), ev.At, got, want)
					}
				}
			}
			crossings := scannedSunCrossings(s.lat, s.lon, []float64{weather.SunHorizonDeg}, from, to)[0]
			matches := func(c sunCrossingAt, in []sunCrossingAt) int {
				n := 0
				for _, o := range in {
					if o.rise == c.rise && o.at.Sub(c.at).Abs() <= time.Minute {
						n++
					}
				}
				return n
			}
			for _, c := range crossings {
				if n := matches(c, fired); n != 1 {
					t.Errorf("real %s at %v popped %d times, want 1", crossingWord(c.rise), c.at, n)
				}
			}
			for _, c := range fired {
				if matches(c, crossings) == 0 {
					t.Errorf("popped %s at %v, no such crossing", crossingWord(c.rise), c.at)
				}
			}
		})
	}
}

func TestSunsetAfterLocalMidnightPops(t *testing.T) {
	c := sunDayCase{name: "Tromsø", lat: 69.65, lon: 18.96, tzKnown: true, tzOff: 2 * 3600,
		now: time.Date(2026, 7, 25, 22, 0, 0, 0, time.UTC)}
	app, pub := c.app(t)
	cfg := app.cfg.Load().Weather
	for at := c.now; at.Before(c.now.Add(time.Hour)); at = at.Add(time.Minute) {
		app.checkSunPopups(context.Background(), at, cfg)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	var texts []string
	for _, p := range pub.notify {
		s, _ := p["text"].(string)
		texts = append(texts, s)
	}
	if len(texts) != 1 || texts[0] != "SUNSET 00:37" {
		t.Errorf("popups from 00:00 to 01:00 local on 2026-07-26: %q, want SUNSET 00:37", texts)
	}
}
