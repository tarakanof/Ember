package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type weatherSurfaces struct {
	dash      weatherStateOut
	knob      *knobWeather
	tileLive  bool
	airLive   bool
	clockMoon bool
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
	}
	return out
}

func TestWeatherConsumersAgreeOnFreshness(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	at := func(age time.Duration) (*weatherObservation, *airObservation) {
		return &weatherObservation{Condition: render.WeatherRain, TempC: 11, FetchedAt: now.Add(-age)},
			&airObservation{AQI: 42, FetchedAt: now.Add(-age)}
	}
	keep := func(*WeatherConfig) {}
	cases := []struct {
		name      string
		mut       func(*WeatherConfig)
		age       time.Duration
		noObs     bool
		noAir     bool
		wantStale bool
		wantLive  bool
		wantKnob  bool
	}{
		{"fresh", keep, 0, false, false, false, true, true},
		{"just under the limit", keep, 30*time.Minute - time.Second, false, false, false, true, true},
		{"at the limit", keep, 30 * time.Minute, false, false, true, false, true},
		{"long stale", keep, 5 * time.Hour, false, false, true, false, true},
		{"disabled", func(c *WeatherConfig) { c.Enabled = false }, 0, false, false, false, false, false},
		{"missing", keep, 0, true, true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, air := at(tc.age)
			if tc.noObs {
				obs = nil
			}
			if tc.noAir {
				air = nil
			}
			s := observeWeatherSurfaces(t, tc.mut, obs, air, now)
			if s.tileLive != tc.wantLive || s.airLive != tc.wantLive {
				t.Errorf("tile live = %v, air tile live = %v, want %v", s.tileLive, s.airLive, tc.wantLive)
			}
			if (s.knob != nil) != tc.wantKnob {
				t.Fatalf("knob weather = %+v, want present %v", s.knob, tc.wantKnob)
			}
			if s.knob != nil && s.knob.Stale != tc.wantStale {
				t.Errorf("knob stale = %v, want %v", s.knob.Stale, tc.wantStale)
			}
			if tc.noObs {
				if s.dash.Current != nil || s.dash.Air != nil {
					t.Errorf("dashboard current/air = %+v / %+v, want null", s.dash.Current, s.dash.Air)
				}
				return
			}
			if s.dash.Current == nil || s.dash.Current.Stale != tc.wantStale {
				t.Errorf("dashboard current = %+v, want stale %v", s.dash.Current, tc.wantStale)
			}
			if s.dash.Air == nil || s.dash.Air.Stale != tc.wantStale {
				t.Errorf("dashboard air = %+v, want stale %v", s.dash.Air, tc.wantStale)
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
			if s.clockMoon != tc.wantMoon {
				t.Errorf("clock moon = %v, want %v", s.clockMoon, tc.wantMoon)
			}
		})
	}
}
