package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type weatherSurfaces struct {
	dash        weatherStateOut
	knob        *knobWeather
	tileLive    bool
	airLive     bool
	clockMoon   bool
	previewMoon bool
}

func observeWeatherSurfaces(t *testing.T, mut func(*WeatherConfig), obs *weatherObservation, air *airObservation, now time.Time) weatherSurfaces {
	t.Helper()
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.MoonPhase = boolPtr(true)
	cfg.Weather.Latitude, cfg.Weather.Longitude = 52.37, 4.9
	mut(&cfg.Weather)
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	app.weather.mu.Lock()
	if obs != nil {
		app.weather.obs, app.weather.have = *obs, true
	}
	if air != nil {
		app.weather.air, app.weather.haveAir = *air, true
	}
	app.weather.mu.Unlock()

	in := app.coord.tileInputs(now)
	out := weatherSurfaces{
		dash:     app.buildWeatherState(now),
		knob:     app.knobWeather(now),
		tileLive: weatherTile.live(&in),
		airLive:  airTile.live(&in),
	}
	if v, ok := weatherTile.view(&in); ok && obs != nil {
		w := app.cfg.Load().Weather
		plain := render.WeatherTileFrame(obs.Condition, weatherTempText(obs.TempC, w.Units), obs.TempC, forecastWindow(obs.Hourly, w.ForecastHours), nil)
		out.clockMoon = v.frame() != plain
		preview := previewCard(t, app.weatherPreview(weatherQuery(w), now), "weather")
		out.previewMoon = !slicesEqualStr(preview, render.HexPixels(&plain))
	}
	return out
}

func TestWeatherConsumersAgreeOnFreshness(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	limit := 30 * time.Minute
	keep := func(*WeatherConfig) {}
	off := func(c *WeatherConfig) { c.Enabled = false }
	cases := []struct {
		name               string
		mut                func(*WeatherConfig)
		obsAge, airAge     time.Duration
		noObs, noAir       bool
		obsStale, airStale bool
		tileLive, airLive  bool
		wantKnob           bool
	}{
		{"fresh", keep, 0, 0, false, false, false, false, true, true, true},
		{"just under the limit", keep, limit - time.Second, limit - time.Second, false, false, false, false, true, true, true},
		{"at the limit", keep, limit, limit, false, false, true, true, false, false, true},
		{"long stale", keep, 5 * time.Hour, 5 * time.Hour, false, false, true, true, false, false, true},
		{"weather fresh, air stale", keep, 0, time.Hour, false, false, false, true, true, false, true},
		{"weather stale, air fresh", keep, time.Hour, 0, false, false, true, false, false, true, true},
		{"disabled", off, 0, 0, false, false, false, false, false, false, false},
		{"missing", keep, 0, 0, true, true, false, false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := &weatherObservation{Condition: render.WeatherRain, TempC: 11, FetchedAt: now.Add(-tc.obsAge)}
			air := &airObservation{AQI: 42, FetchedAt: now.Add(-tc.airAge)}
			if tc.noObs {
				obs = nil
			}
			if tc.noAir {
				air = nil
			}
			s := observeWeatherSurfaces(t, tc.mut, obs, air, now)
			if s.tileLive != tc.tileLive || s.airLive != tc.airLive {
				t.Errorf("tile live = %v, air tile live = %v, want %v, %v", s.tileLive, s.airLive, tc.tileLive, tc.airLive)
			}
			if (s.knob != nil) != tc.wantKnob {
				t.Fatalf("knob weather = %+v, want present %v", s.knob, tc.wantKnob)
			}
			if s.knob != nil && s.knob.Stale != tc.obsStale {
				t.Errorf("knob stale = %v, want %v", s.knob.Stale, tc.obsStale)
			}
			if tc.noObs {
				if s.dash.Current != nil || s.dash.Air != nil {
					t.Errorf("dashboard current/air = %+v / %+v, want null", s.dash.Current, s.dash.Air)
				}
				return
			}
			if s.dash.Current == nil || s.dash.Current.Stale != tc.obsStale {
				t.Errorf("dashboard current = %+v, want stale %v", s.dash.Current, tc.obsStale)
			}
			if s.dash.Air == nil || s.dash.Air.Stale != tc.airStale {
				t.Errorf("dashboard air = %+v, want stale %v", s.dash.Air, tc.airStale)
			}
		})
	}
}

func TestWeatherConsumersAgreeOnSunAndNight(t *testing.T) {
	clearSky := func(now time.Time) *weatherObservation {
		return &weatherObservation{Condition: render.WeatherClear, TempC: 5, FetchedAt: now}
	}
	amsterdam := func(*WeatherConfig) {}
	svalbard := func(c *WeatherConfig) { c.Latitude, c.Longitude = 78.2, 15.6 }
	noCoords := func(c *WeatherConfig) { c.Latitude, c.Longitude = 0, 0 }
	cases := []struct {
		name      string
		mut       func(*WeatherConfig)
		now       time.Time
		wantSun   bool
		wantNight bool
		wantMoon  bool
	}{
		{"day", amsterdam, time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC), true, false, false},
		{"night", amsterdam, time.Date(2026, 9, 26, 22, 0, 0, 0, time.UTC), true, true, true},
		{"before sunrise", amsterdam, time.Date(2026, 9, 26, 4, 0, 0, 0, time.UTC), true, true, true},
		{"no coordinates", noCoords, time.Date(2026, 9, 26, 22, 0, 0, 0, time.UTC), false, false, false},
		{"McMurdo after the last sunset", func(c *WeatherConfig) { c.Latitude, c.Longitude = -77.8, 166.7 }, time.Date(2026, 4, 25, 5, 0, 0, 0, time.UTC), true, true, true},
		{"68N before the first sunrise", func(c *WeatherConfig) { c.Latitude, c.Longitude = 68, 0 }, time.Date(2026, 1, 4, 5, 0, 0, 0, time.UTC), true, true, true},
		{"Tromsø next to polar day", func(c *WeatherConfig) { c.Latitude, c.Longitude = 69.65, 18.96 }, time.Date(2026, 5, 19, 10, 41, 0, 0, time.UTC), false, false, false},
		{"McMurdo in polar night", func(c *WeatherConfig) { c.Latitude, c.Longitude = -77.8, 166.7 }, time.Date(2027, 8, 18, 12, 0, 0, 0, time.UTC), false, true, false},
		{"polar night", svalbard, time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), false, true, false},
		{"polar day", svalbard, time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := observeWeatherSurfaces(t, tc.mut, clearSky(tc.now), nil, tc.now)
			if (s.dash.Sun != nil) != tc.wantSun {
				t.Errorf("dashboard sun = %+v, want present %v", s.dash.Sun, tc.wantSun)
			}
			if s.knob == nil {
				t.Fatal("knob weather = nil")
			}
			if (s.knob.Sunrise != nil) != tc.wantSun || (s.knob.Sunset != nil) != tc.wantSun {
				t.Errorf("knob sun = %v/%v, want present %v", s.knob.Sunrise, s.knob.Sunset, tc.wantSun)
			}
			if tc.wantSun && (*s.knob.Sunrise != s.dash.Sun.Sunrise.Unix() || *s.knob.Sunset != s.dash.Sun.Sunset.Unix()) {
				t.Errorf("knob sun %d/%d, dashboard %v/%v", *s.knob.Sunrise, *s.knob.Sunset, s.dash.Sun.Sunrise, s.dash.Sun.Sunset)
			}
			if s.knob.Night != tc.wantNight {
				t.Errorf("knob night = %v, want %v", s.knob.Night, tc.wantNight)
			}
			if s.clockMoon != tc.wantMoon || s.previewMoon != tc.wantMoon {
				t.Errorf("clock moon = %v, preview moon = %v, want %v", s.clockMoon, s.previewMoon, tc.wantMoon)
			}
		})
	}
}

func TestClockMoonFollowsTheSunSchedule(t *testing.T) {
	amsterdam := func(*WeatherConfig) {}
	tromso := func(c *WeatherConfig) { c.Latitude, c.Longitude = 69.65, 18.96 }
	cest := weatherObservation{Condition: render.WeatherClear, TempC: 12, TZKnown: true, TZOffsetSeconds: 2 * 3600}
	day := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	_, sunset, ok := localSunTimes(52.37, 4.9, day, cest)
	if !ok {
		t.Fatal("fixture: no Amsterdam sunset")
	}
	cases := []struct {
		name      string
		mut       func(*WeatherConfig)
		now       time.Time
		wantNight bool
	}{
		{"a second before sunset", amsterdam, sunset.Add(-time.Second), false},
		{"exactly at sunset", amsterdam, sunset, true},
		{"Tromsø 00:20 local, sun still up until 00:36", tromso, time.Date(2026, 7, 26, 22, 20, 0, 0, time.UTC), false},
		{"Tromsø 00:50 local, after the 00:36 sunset", tromso, time.Date(2026, 7, 26, 22, 50, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := cest
			obs.FetchedAt = tc.now
			s := observeWeatherSurfaces(t, tc.mut, &obs, nil, tc.now)
			if s.knob == nil || s.knob.Night != tc.wantNight {
				t.Fatalf("knob weather = %+v, want night %v", s.knob, tc.wantNight)
			}
			if s.clockMoon != tc.wantNight || s.previewMoon != tc.wantNight {
				t.Errorf("clock moon = %v, preview moon = %v, want %v", s.clockMoon, s.previewMoon, tc.wantNight)
			}
		})
	}
}
