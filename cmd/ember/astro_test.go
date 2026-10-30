package main

import (
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

type sunCrossingAt struct {
	at   time.Time
	rise bool
}

func scannedSunCrossings(lat, lon float64, alts []float64, from, to time.Time) [][]sunCrossingAt {
	out := make([][]sunCrossingAt, len(alts))
	above := make([]bool, len(alts))
	e := weather.SolarElevation(lat, lon, from)
	for i, alt := range alts {
		above[i] = e > alt
	}
	for at := from.Add(time.Minute); at.Before(to); at = at.Add(time.Minute) {
		e := weather.SolarElevation(lat, lon, at)
		for i, alt := range alts {
			if a := e > alt; a != above[i] {
				out[i] = append(out[i], sunCrossingAt{at.Add(-30 * time.Second), a})
				above[i] = a
			}
		}
	}
	return out
}

type sunEventsByDate struct {
	lat, lon float64
	day      time.Time
	have     bool
	evs      []weather.SunEvent
}

func (c *sunEventsByDate) at(now time.Time) []weather.SunEvent {
	if day := now.UTC().Truncate(24 * time.Hour); !c.have || !day.Equal(c.day) {
		c.evs, c.day, c.have = weather.SunEvents(c.lat, c.lon, now), day, true
	}
	return c.evs
}

func crossingWord(rise bool) string {
	if rise {
		return "rising"
	}
	return "setting"
}
