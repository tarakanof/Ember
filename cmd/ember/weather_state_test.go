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

func TestNightNextToPolarNight(t *testing.T) {
	cases := []struct {
		name     string
		lat, lon float64
		now      time.Time
	}{
		{"McMurdo after the last sunset", -77.8, 166.7, time.Date(2026, 4, 25, 5, 0, 0, 0, time.UTC)},
		{"68N before the first sunrise", 68, 0, time.Date(2026, 1, 4, 5, 0, 0, 0, time.UTC)},
	}
	bc := BrightnessConfig{DayLevel: 255, NightLevel: 20, TwilightMinutes: 30}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWeatherState(WeatherConfig{Enabled: true, Latitude: tc.lat, Longitude: tc.lon}, weatherObservation{}, true, airObservation{}, false, tc.now)
			if !s.Night {
				t.Errorf("weather state night = false, want true (sun %v %v..%v)", s.HaveSun, s.Sunrise, s.Sunset)
			}
			if level, night := sunLevel(bc, tc.lat, tc.lon, tc.now); !night || level != bc.NightLevel {
				t.Errorf("sunLevel = %d,%v, want %d,true", level, night, bc.NightLevel)
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
