package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type sunDayCase struct {
	name     string
	lat, lon float64
	tzKnown  bool
	tzOff    int
	now      time.Time
}

func sunDayCases() []sunDayCase {
	cest := time.FixedZone("CEST", 2*3600)
	pdt := time.FixedZone("PDT", -7*3600)
	berlinLat, berlinLon := 52.52, 13.405
	laLat, laLon := 34.05, -118.24
	londonLat, londonLon := 51.5, -0.1
	var out []sunDayCase
	for _, known := range []bool{true, false} {
		suffix := " tz known"
		if !known {
			suffix = " tz unknown"
		}
		out = append(out,
			sunDayCase{"CEST 00:30" + suffix, berlinLat, berlinLon, known, 2 * 3600, time.Date(2026, 7, 15, 0, 30, 0, 0, cest)},
			sunDayCase{"CEST 23:30" + suffix, berlinLat, berlinLon, known, 2 * 3600, time.Date(2026, 7, 15, 23, 30, 0, 0, cest)},
			sunDayCase{"PDT 18:00" + suffix, laLat, laLon, known, -7 * 3600, time.Date(2026, 7, 15, 18, 0, 0, 0, pdt)},
			sunDayCase{"PDT 23:30" + suffix, laLat, laLon, known, -7 * 3600, time.Date(2026, 7, 15, 23, 30, 0, 0, pdt)},
			sunDayCase{"UTC 00:30" + suffix, londonLat, londonLon, known, 0, time.Date(2026, 1, 15, 0, 30, 0, 0, time.UTC)},
			sunDayCase{"UTC 23:30" + suffix, londonLat, londonLon, known, 0, time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)},
		)
	}
	nzdt := time.FixedZone("NZDT", 13*3600)
	lint := time.FixedZone("LINT", 14*3600)
	lonMinus175 := time.FixedZone("lon-175", -12*3600)
	aucklandLat, aucklandLon := -36.85, 174.76
	kiritimatiLat, kiritimatiLon := 1.87, -157.4
	for _, hm := range [][2]int{{13, 0}, {0, 30}, {23, 30}} {
		label := fmt.Sprintf(" %02d:%02d", hm[0], hm[1])
		out = append(out,
			sunDayCase{"NZDT" + label, aucklandLat, aucklandLon, true, 13 * 3600, time.Date(2026, 1, 15, hm[0], hm[1], 0, 0, nzdt)},
			sunDayCase{"Kiritimati" + label, kiritimatiLat, kiritimatiLon, true, 14 * 3600, time.Date(2026, 1, 15, hm[0], hm[1], 0, 0, lint)},
			sunDayCase{"lon -175 tz unknown" + label, -20, -175, false, 0, time.Date(2026, 1, 15, hm[0], hm[1], 0, 0, lonMinus175)},
		)
	}
	return out
}

func (c sunDayCase) offset() time.Duration {
	if c.tzKnown {
		return time.Duration(c.tzOff) * time.Second
	}
	return time.Duration(math.Round(c.lon/15)) * time.Hour
}

func (c sunDayCase) localDate(t time.Time) string {
	return t.UTC().Add(c.offset()).Format("2006-01-02")
}

func (c sunDayCase) app(t *testing.T) (*App, *recordingPublisher) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Provider = "open-meteo"
	cfg.Weather.SunPopups = boolPtr(true)
	cfg.Weather.MoonPhase = boolPtr(true)
	cfg.Weather.Latitude, cfg.Weather.Longitude = c.lat, c.lon
	app := NewApp(cfg, pub, testLogger())
	app.weather.obs = weatherObservation{
		Condition: render.WeatherClear, TempC: 10, FetchedAt: c.now,
		TZOffsetSeconds: c.tzOff, TZKnown: c.tzKnown,
	}
	if !c.tzKnown {
		app.weather.obs.TZOffsetSeconds = 0
	}
	app.weather.have = true
	return app, pub
}

func TestSunTimesUseTheLocalDayEverywhere(t *testing.T) {
	for _, c := range sunDayCases() {
		t.Run(c.name, func(t *testing.T) {
			app, _ := c.app(t)
			today := c.localDate(c.now)

			sun := app.buildWeatherState(c.now).Sun
			if sun == nil {
				t.Fatal("dashboard: no sun times")
			}
			rise, set := sun.Sunrise, sun.Sunset
			if got := c.localDate(rise); got != today {
				t.Errorf("dashboard sunrise %v is on local day %s, want %s", rise, got, today)
			}
			if got := c.localDate(set); got != today {
				t.Errorf("dashboard sunset %v is on local day %s, want %s", set, got, today)
			}

			kw := app.knobWeather(c.now)
			if kw == nil || kw.Sunrise == nil || kw.Sunset == nil {
				t.Fatal("knob view: no sun times")
			}
			if *kw.Sunrise != rise.Unix() || *kw.Sunset != set.Unix() {
				t.Errorf("knob sun %d/%d, dashboard %d/%d", *kw.Sunrise, *kw.Sunset, rise.Unix(), set.Unix())
			}

			wantNight := c.now.Before(rise) || c.now.After(set)
			cfg := app.cfg.Load().Weather
			if gotNight := weatherTileMoon(cfg, app.weather.state(cfg, c.now), c.now) != nil; gotNight != wantNight {
				t.Errorf("clock night = %v, want %v (sunrise %v, sunset %v)", gotNight, wantNight, rise, set)
			}

			local := c.now.UTC().Add(c.offset())
			noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, time.UTC).Add(-c.offset())
			if weatherTileMoon(cfg, app.weather.state(cfg, noon), noon) != nil {
				t.Errorf("clock night at local noon %v (sunrise %v, sunset %v)", noon, rise, set)
			}
		})
	}
}

func TestSunPopupsFireOnTheLocalDaysEvents(t *testing.T) {
	for _, c := range sunDayCases() {
		t.Run(c.name, func(t *testing.T) {
			app, pub := c.app(t)
			sun := app.buildWeatherState(c.now).Sun
			if sun == nil {
				t.Fatal("dashboard: no sun times")
			}
			cfg := app.cfg.Load().Weather
			for _, ev := range []struct {
				word string
				at   time.Time
				done *string
			}{
				{"SUNRISE", sun.Sunrise, &app.weather.sunriseDoneDay},
				{"SUNSET", sun.Sunset, &app.weather.sunsetDoneDay},
			} {
				pub.mu.Lock()
				before := len(pub.notify)
				pub.mu.Unlock()
				var fireAt time.Time
				for at := ev.at.Add(-3 * time.Minute); !at.After(ev.at.Add(3 * time.Minute)); at = at.Add(15 * time.Second) {
					app.checkSunPopups(context.Background(), at, cfg)
					pub.mu.Lock()
					n := len(pub.notify)
					pub.mu.Unlock()
					if fireAt.IsZero() && n > before {
						fireAt = at
					}
				}
				pub.mu.Lock()
				fired := pub.notify[before:]
				pub.mu.Unlock()
				if len(fired) != 1 {
					t.Fatalf("%s near %v: fired %d popups, want 1", ev.word, ev.at, len(fired))
				}
				txt, _ := fired[0]["text"].(string)
				clock, ok := strings.CutPrefix(txt, ev.word+" ")
				if !ok {
					t.Fatalf("popup text = %q, want %s HH:MM", txt, ev.word)
				}
				hm, err := time.Parse("15:04", clock)
				if err != nil {
					t.Fatalf("popup text = %q: %v", txt, err)
				}
				local := ev.at.UTC().Add(c.offset())
				labelled := time.Date(local.Year(), local.Month(), local.Day(), hm.Hour(), hm.Minute(), 0, 0, time.UTC)
				if d := labelled.Sub(local); d < -3*time.Minute || d > 3*time.Minute {
					t.Errorf("popup text = %q, dashboard says %s", txt, local.Format("15:04"))
				}
				app.weather.mu.RLock()
				key := *ev.done
				app.weather.mu.RUnlock()
				if want := c.localDate(fireAt); key != want {
					t.Errorf("%s dedupe key = %q, want local date %q", ev.word, key, want)
				}
			}
		})
	}
}
