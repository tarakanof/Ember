package main

import "time"

const weatherStaleAfter = 30 * time.Minute

const sunRounding = 5 * time.Minute

type weatherState struct {
	Enabled bool

	Obs     weatherObservation
	HaveObs bool
	Fresh   bool

	Air      airObservation
	HaveAir  bool
	AirFresh bool

	LocalDay string
	Night    bool
	HaveSun  bool
	Sunrise  time.Time
	Sunset   time.Time
}

func newWeatherState(cfg WeatherConfig, obs weatherObservation, haveObs bool, air airObservation, haveAir bool, now time.Time) weatherState {
	s := weatherState{
		Enabled:  cfg.Enabled,
		Obs:      obs,
		HaveObs:  haveObs,
		Fresh:    haveObs && now.Sub(obs.FetchedAt) < weatherStaleAfter,
		Air:      air,
		HaveAir:  haveAir,
		AirFresh: haveAir && now.Sub(air.FetchedAt) < weatherStaleAfter,
		LocalDay: localDay(now, obs, cfg.Longitude),
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		s.Night = sunNight(cfg.Latitude, cfg.Longitude, now)
		s.Sunrise, s.Sunset, s.HaveSun = localSunTimes(cfg.Latitude, cfg.Longitude, now, obs)
	}
	return s
}

func (s *weatherStore) state(cfg WeatherConfig, now time.Time) weatherState {
	s.mu.RLock()
	obs, haveObs, air, haveAir := s.obs, s.have, s.air, s.haveAir
	s.mu.RUnlock()
	return newWeatherState(cfg, obs, haveObs, air, haveAir, now)
}

func (s weatherState) live() bool { return s.Enabled && s.Fresh }

func (s weatherState) airLive() bool { return s.Enabled && s.AirFresh }

func (s weatherState) roundedSun() (sunrise, sunset time.Time) {
	return s.Sunrise.Round(sunRounding), s.Sunset.Round(sunRounding)
}
