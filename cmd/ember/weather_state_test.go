package main

import (
	"testing"
	"time"
)

func TestWeatherStateFreshness(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	on := WeatherConfig{Enabled: true, Latitude: 52.37, Longitude: 4.9}
	off := on
	off.Enabled = false
	noCoords := WeatherConfig{Enabled: true}
	obsAged := func(age time.Duration) weatherObservation { return weatherObservation{FetchedAt: now.Add(-age)} }
	airAged := func(age time.Duration) airObservation { return airObservation{FetchedAt: now.Add(-age)} }
	cases := []struct {
		name               string
		cfg                WeatherConfig
		obs                weatherObservation
		haveObs            bool
		air                airObservation
		haveAir            bool
		fresh, airFresh    bool
		live, airLive      bool
		hasCoords, haveSun bool
	}{
		{"fresh", on, obsAged(0), true, airAged(0), true, true, true, true, true, true, true},
		{"just under the limit", on, obsAged(weatherStaleAfter - time.Second), true, airAged(weatherStaleAfter - time.Second), true, true, true, true, true, true, true},
		{"at the limit", on, obsAged(weatherStaleAfter), true, airAged(weatherStaleAfter), true, false, false, false, false, true, true},
		{"air stale, weather fresh", on, obsAged(time.Minute), true, airAged(time.Hour), true, true, false, true, false, true, true},
		{"weather stale, air fresh", on, obsAged(time.Hour), true, airAged(time.Minute), true, false, true, false, true, true, true},
		{"missing", on, weatherObservation{}, false, airObservation{}, false, false, false, false, false, true, true},
		{"missing with a recent stamp", on, obsAged(0), false, airAged(0), false, false, false, false, false, true, true},
		{"disabled", off, obsAged(0), true, airAged(0), true, true, true, false, false, true, true},
		{"no coordinates", noCoords, obsAged(0), true, airAged(0), true, true, true, true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWeatherState(tc.cfg, tc.obs, tc.haveObs, tc.air, tc.haveAir, now)
			if s.Fresh != tc.fresh || s.AirFresh != tc.airFresh {
				t.Errorf("fresh = %v / air %v, want %v / %v", s.Fresh, s.AirFresh, tc.fresh, tc.airFresh)
			}
			if s.live() != tc.live || s.airLive() != tc.airLive {
				t.Errorf("live = %v / air %v, want %v / %v", s.live(), s.airLive(), tc.live, tc.airLive)
			}
			if s.HaveSun != tc.haveSun {
				t.Errorf("sun = %v, want %v", s.HaveSun, tc.haveSun)
			}
			if !tc.hasCoords && s.Night {
				t.Error("night without coordinates")
			}
		})
	}
}

func TestWeatherStateNightBoundaries(t *testing.T) {
	cfg := WeatherConfig{Enabled: true, Latitude: 52.37, Longitude: 4.9}
	obs := weatherObservation{TZKnown: true, TZOffsetSeconds: 2 * 3600}
	day := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	rise, set, ok := localSunTimes(cfg.Latitude, cfg.Longitude, day, obs)
	if !ok {
		t.Fatal("fixture: no sun times for Amsterdam")
	}
	cases := []struct {
		name  string
		now   time.Time
		night bool
	}{
		{"a second before sunrise", rise.Add(-time.Second), true},
		{"at sunrise", rise, false},
		{"midday", day, false},
		{"a second before sunset", set.Add(-time.Second), false},
		{"at sunset", set, true},
		{"a second after sunset", set.Add(time.Second), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWeatherState(cfg, obs, true, airObservation{}, false, tc.now)
			if s.Night != tc.night {
				t.Errorf("night = %v, want %v (sunrise %v, sunset %v)", s.Night, tc.night, rise, set)
			}
			if !s.HaveSun || !s.Sunrise.Equal(rise) || !s.Sunset.Equal(set) {
				t.Errorf("sun = %v %v..%v, want %v..%v", s.HaveSun, s.Sunrise, s.Sunset, rise, set)
			}
			r, st := s.roundedSun()
			if r.Unix()%int64(sunRounding/time.Second) != 0 || st.Unix()%int64(sunRounding/time.Second) != 0 {
				t.Errorf("rounded sun %v / %v not on a %v boundary", r, st, sunRounding)
			}
		})
	}
}

func TestNightAtPolarEdges(t *testing.T) {
	cases := []struct {
		name     string
		lat, lon float64
		now      time.Time
		night    bool
	}{
		{"Tromsø next to polar day, sun at 40°", 69.65, 18.96, time.Date(2026, 5, 19, 10, 41, 0, 0, time.UTC), false},
		{"Mawson next to polar day, sun at 44°", -67.6, 62.87, time.Date(2026, 11, 30, 7, 37, 0, 0, time.UTC), false},
		{"McMurdo after the last sunset", -77.8, 166.7, time.Date(2026, 4, 25, 5, 0, 0, 0, time.UTC), true},
		{"68N before the first sunrise", 68, 0, time.Date(2026, 1, 4, 5, 0, 0, 0, time.UTC), true},
		{"McMurdo in polar night, sun at -25°", -77.8, 166.7, time.Date(2027, 8, 18, 12, 0, 0, 0, time.UTC), true},
		{"Svalbard in polar day", 78.2, 15.6, time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), false},
	}
	bc := BrightnessConfig{DayLevel: 255, NightLevel: 20, TwilightMinutes: 30}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWeatherState(WeatherConfig{Enabled: true, Latitude: tc.lat, Longitude: tc.lon}, weatherObservation{}, true, airObservation{}, false, tc.now)
			if s.Night != tc.night {
				t.Errorf("weather state night = %v, want %v (elevation %.1f°)", s.Night, tc.night, solarElevation(tc.lat, tc.lon, tc.now))
			}
			want := bc.DayLevel
			if tc.night {
				want = bc.NightLevel
			}
			if level, night := sunLevel(bc, tc.lat, tc.lon, tc.now); night != tc.night || level != want {
				t.Errorf("sunLevel = %d,%v, want %d,%v", level, night, want, tc.night)
			}
		})
	}
}

func TestSolarElevationMatchesSunTimesHorizon(t *testing.T) {
	for _, c := range [][2]float64{{52.52, 13.405}, {34.05, -118.24}, {-33.87, 151.21}, {69.65, 18.96}, {-77.8, 166.7}} {
		for d := 0; d < 365; d += 30 {
			rise, set, ok := sunTimes(c[0], c[1], time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, d))
			if !ok {
				continue
			}
			for _, at := range []time.Time{rise, set} {
				if e := solarElevation(c[0], c[1], at); e < sunHorizonDeg-0.05 || e > sunHorizonDeg+0.05 {
					t.Errorf("%v at %v: elevation %.3f°, want ≈ %.3f°", c, at, e, sunHorizonDeg)
				}
			}
		}
	}
}

func sunYearStep(t *testing.T) time.Duration {
	if testing.Short() {
		return 97 * time.Minute
	}
	return 7 * time.Minute
}

func TestSunNightMatchesElevationAllYearAtPolarSites(t *testing.T) {
	sites := map[string][2]float64{
		"Tromsø": {69.65, 18.96}, "68N": {68, 0}, "Svalbard": {78.2, 15.6}, "Alert": {82.5, -62.3},
		"Utqiagvik": {71.29, -156.79}, "McMurdo": {-77.8, 166.7}, "Mawson": {-67.6, 62.87}, "66.6S": {-66.6, -140},
	}
	bc := BrightnessConfig{DayLevel: 255, NightLevel: 20, TwilightMinutes: 30}
	step := sunYearStep(t)
	for name, c := range sites {
		t.Run(name, func(t *testing.T) {
			wrong := 0
			for at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); at.Year() == 2026; at = at.Add(step) {
				night := sunNight(c[0], c[1], at)
				if _, levelNight := sunLevel(bc, c[0], c[1], at); levelNight != night {
					t.Fatalf("%v: sunLevel night %v, sunNight %v", at, levelNight, night)
				}
				e := solarElevation(c[0], c[1], at)
				if e-sunHorizonDeg > 1 || sunHorizonDeg-e > 1 {
					if night != (e < sunHorizonDeg) {
						wrong++
						if wrong <= 3 {
							t.Errorf("%v: night %v with the sun at %.1f°", at, night, e)
						}
					}
				}
			}
			if wrong > 0 {
				t.Errorf("%d samples disagree with the sun's elevation", wrong)
			}
		})
	}
}

func TestSunNightAtNormalLatitudesStaysOnTheEventBracket(t *testing.T) {
	sites := map[string][2]float64{"Berlin": {52.52, 13.405}, "LA": {34.05, -118.24}, "Sydney": {-33.87, 151.21}}
	step := sunYearStep(t)
	for name, c := range sites {
		t.Run(name, func(t *testing.T) {
			for at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); at.Year() == 2026; at = at.Add(step) {
				last, next := sunEventsAround(c[0], c[1], at)
				if last == nil || next == nil || last.rise == next.rise {
					t.Fatalf("%v: bracket %+v..%+v, want a sunrise and a sunset on either side", at, last, next)
				}
				if sunNight(c[0], c[1], at) != !last.rise {
					t.Fatalf("%v: night must be the bracket's call", at)
				}
			}
		})
	}
}

func TestWeatherStatePolar(t *testing.T) {
	cfg := WeatherConfig{Enabled: true, Latitude: 78.2, Longitude: 15.6}
	cases := []struct {
		name  string
		now   time.Time
		night bool
	}{
		{"polar night", time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), true},
		{"polar day", time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWeatherState(cfg, weatherObservation{}, true, airObservation{}, false, tc.now)
			if s.HaveSun {
				t.Errorf("sun times %v..%v, want none", s.Sunrise, s.Sunset)
			}
			if s.Night != tc.night {
				t.Errorf("night = %v, want %v", s.Night, tc.night)
			}
		})
	}
}

func TestWeatherStoreStateSnapshotsTheStore(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s := newWeatherStore()
	if got := s.state(WeatherConfig{Enabled: true}, now); got.HaveObs || got.HaveAir || got.live() || got.airLive() {
		t.Fatalf("empty store state = %+v, want nothing live", got)
	}
	s.mu.Lock()
	s.obs, s.have = weatherObservation{Condition: "rain", FetchedAt: now, Hourly: []float64{1, 2}}, true
	s.air, s.haveAir = airObservation{AQI: 42, FetchedAt: now, HourlyAQI: []float64{42, 40}}, true
	s.mu.Unlock()
	got := s.state(WeatherConfig{Enabled: true}, now)
	if !got.live() || !got.airLive() || got.Obs.Condition != "rain" || got.Air.AQI != 42 ||
		len(got.Obs.Hourly) != 2 || len(got.Air.HourlyAQI) != 2 {
		t.Errorf("state = %+v, want the stored observations live", got)
	}
}
