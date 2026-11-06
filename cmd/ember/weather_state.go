package main

import (
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

const weatherStaleAfter = 30 * time.Minute

const sunRounding = 5 * time.Minute

type weatherState struct {
	Enabled bool

	Obs     weather.Observation
	HaveObs bool
	Fresh   bool

	Air      weather.AirObservation
	HaveAir  bool
	AirFresh bool

	LocalDay string
	Night    bool
	Sun      weather.SunCrossings

	At           time.Time
	Lat, Lon     float64
	HaveLocation bool
}

func newWeatherState(cfg weather.Config, obs weather.Observation, haveObs bool, air weather.AirObservation, haveAir bool, now time.Time) weatherState {
	obs = obs.Current(now)
	s := weatherState{
		Enabled:  cfg.Enabled,
		Obs:      obs,
		HaveObs:  haveObs,
		Fresh:    haveObs && now.Sub(later(obs.FetchedAt, obs.ValidUntil)) < weatherStaleAfter,
		Air:      air,
		HaveAir:  haveAir,
		AirFresh: haveAir && now.Sub(air.FetchedAt) < weatherStaleAfter,
		LocalDay: weather.LocalDay(now, obs, cfg.Longitude),
		At:       now,
		Lat:      cfg.Latitude,
		Lon:      cfg.Longitude,
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		s.HaveLocation = true
		s.Night = weather.SunNight(cfg.Latitude, cfg.Longitude, now)
		s.Sun = weather.LocalSunTimes(cfg.Latitude, cfg.Longitude, now, obs)
	}
	return s
}

func (s *weatherStore) state(cfg weather.Config, now time.Time) weatherState {
	obs, haveObs, air, haveAir := s.snapshot(cfg.Source())
	return newWeatherState(cfg, obs, haveObs, air, haveAir, now)
}

func (s *weatherStore) snapshot(src weather.Source) (obs weather.Observation, haveObs bool, air weather.AirObservation, haveAir bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.source == src {
		obs, haveObs = s.obs, s.have
	}
	if s.source.Loc == src.Loc {
		air, haveAir = s.air, s.haveAir
	}
	return obs, haveObs, air, haveAir
}

func (s weatherState) live() bool { return s.Enabled && s.Fresh }

func (s weatherState) airLive() bool { return s.Enabled && s.AirFresh }

func (s weatherState) roundedSun() (sunrise, sunset time.Time, ok bool) {
	if !s.Sun.Both() {
		return time.Time{}, time.Time{}, false
	}
	return s.Sun.Rise.Round(sunRounding), s.Sun.Set.Round(sunRounding), true
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
