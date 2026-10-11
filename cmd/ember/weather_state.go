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

	At           time.Time
	Lat, Lon     float64
	HaveLocation bool
}

func newWeatherState(cfg WeatherConfig, obs weatherObservation, haveObs bool, air airObservation, haveAir bool, now time.Time) weatherState {
	obs = obs.current(now)
	s := weatherState{
		Enabled:  cfg.Enabled,
		Obs:      obs,
		HaveObs:  haveObs,
		Fresh:    haveObs && now.Sub(later(obs.FetchedAt, obs.ValidUntil)) < weatherStaleAfter,
		Air:      air,
		HaveAir:  haveAir,
		AirFresh: haveAir && now.Sub(air.FetchedAt) < weatherStaleAfter,
		LocalDay: localDay(now, obs, cfg.Longitude),
		At:       now,
		Lat:      cfg.Latitude,
		Lon:      cfg.Longitude,
	}
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		s.HaveLocation = true
		s.Night = sunNight(cfg.Latitude, cfg.Longitude, now)
		s.Sunrise, s.Sunset, s.HaveSun = localSunTimes(cfg.Latitude, cfg.Longitude, now, obs)
	}
	return s
}

func (s *weatherStore) state(cfg WeatherConfig, now time.Time) weatherState {
	obs, haveObs, air, haveAir := s.snapshot(cfg.source())
	return newWeatherState(cfg, obs, haveObs, air, haveAir, now)
}

func (s *weatherStore) snapshot(src weatherSource) (obs weatherObservation, haveObs bool, air airObservation, haveAir bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.source == src {
		obs, haveObs = s.obs, s.have
	}
	if s.source.loc == src.loc {
		air, haveAir = s.air, s.haveAir
	}
	return obs, haveObs, air, haveAir
}

func (s weatherState) live() bool { return s.Enabled && s.Fresh }

func (s weatherState) airLive() bool { return s.Enabled && s.AirFresh }

func (s weatherState) roundedSun() (sunrise, sunset time.Time) {
	return s.Sunrise.Round(sunRounding), s.Sunset.Round(sunRounding)
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
