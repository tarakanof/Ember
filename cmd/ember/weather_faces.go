package main

import (
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

type weatherFaces struct {
	UTCOffset   time.Duration
	RainNow     int
	HaveRainNow bool
	RainUntil   time.Time
	Spells      []weather.RainSpell
	Today       *weather.Day
	Days        []weather.Day
	HaveAstro   bool
	Twilight    weather.Twilight
	MoonPhase   float64
	MoonIllum   float64
	MoonUp      bool
	MoonRise    time.Time
	MoonSet     time.Time
}

func (s weatherState) faces() weatherFaces {
	f := weatherFaces{UTCOffset: weather.LocalOffset(s.Obs, s.Lon)}
	f.RainNow, f.HaveRainNow, f.RainUntil, f.Spells = weather.RainSpells(s.Obs.Precip, s.Obs.PrecipStep, s.At)
	f.Today, f.Days = weather.FaceDays(s.Obs, s.At)
	f.MoonPhase, f.MoonIllum = weather.MoonPhaseAt(s.At)
	if s.HaveLocation {
		f.HaveAstro = true
		f.Twilight = weather.LocalTwilight(s.Lat, s.Lon, s.At, s.Obs)
		f.MoonRise, f.MoonSet, f.MoonUp = weather.MoonUpWindow(s.Lat, s.Lon, s.At)
	}
	return f
}
