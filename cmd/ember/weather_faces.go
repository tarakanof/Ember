package main

import "time"

const (
	rainWetMMh       = 0.1
	rainLightMMh     = 2.5
	rainModerateMMh  = 7.6
	rainSpellHorizon = 12 * time.Hour
	rainSpellMerge   = 30 * time.Minute
	rainSpellsMax    = 4
	faceForecastDays = 3
)

type rainSpell struct {
	Start, End time.Time
	Intensity  int
	Snow       bool
}

type weatherFaces struct {
	UTCOffset   time.Duration
	RainNow     int
	HaveRainNow bool
	RainUntil   time.Time
	Spells      []rainSpell
	Today       *weatherDay
	Days        []weatherDay
	HaveAstro   bool
	Twilight    twilight
	MoonPhase   float64
	MoonIllum   float64
	MoonUp      bool
	MoonRise    time.Time
	MoonSet     time.Time
}

func rainIntensity(mmh float64) int {
	switch {
	case mmh < rainWetMMh:
		return 0
	case mmh < rainLightMMh:
		return 1
	case mmh < rainModerateMMh:
		return 2
	default:
		return 3
	}
}

func (s weatherState) faces() weatherFaces {
	f := weatherFaces{UTCOffset: localOffset(s.Obs, s.Lon)}
	f.RainNow, f.HaveRainNow, f.RainUntil, f.Spells = rainSpells(s.Obs.Precip, s.Obs.PrecipStep, s.At)
	f.Today, f.Days = faceDays(s.Obs, s.At)
	f.MoonPhase, f.MoonIllum = moonPhaseAt(s.At)
	if s.HaveLocation {
		f.HaveAstro = true
		f.Twilight = localTwilight(s.Lat, s.Lon, s.At, s.Obs)
		f.MoonRise, f.MoonSet, f.MoonUp = moonUpWindow(s.Lat, s.Lon, s.At)
	}
	return f
}

func rainSpells(steps []precipStep, step time.Duration, now time.Time) (rainNow int, haveNow bool, until time.Time, spells []rainSpell) {
	type run struct {
		rainSpell
		wet, snowy int
	}
	var runs []run
	for _, p := range steps {
		end := p.At.Add(step)
		if !p.At.After(now) && now.Before(end) {
			rainNow, haveNow = rainIntensity(p.MMh), true
		}
		level := rainIntensity(p.MMh)
		if level == 0 {
			continue
		}
		snow := 0
		if p.Snow {
			snow = 1
		}
		if n := len(runs); n > 0 && !p.At.After(runs[n-1].End.Add(rainSpellMerge)) {
			r := &runs[n-1]
			r.End = end
			r.Intensity = max(r.Intensity, level)
			r.wet++
			r.snowy += snow
			continue
		}
		runs = append(runs, run{rainSpell: rainSpell{Start: p.At, End: end, Intensity: level}, wet: 1, snowy: snow})
	}
	spells = []rainSpell{}
	horizon := now.Add(rainSpellHorizon)
	var edge time.Time
	if n := len(steps); n > 0 {
		edge = steps[n-1].At.Add(step)
	}
	for _, r := range runs {
		if !r.End.After(now) || !r.Start.Before(horizon) || len(spells) == rainSpellsMax {
			continue
		}
		r.Snow = 2*r.snowy > r.wet
		spells = append(spells, r.rainSpell)
		if rainNow > 0 && !r.Start.After(now) && r.End.Before(edge) {
			until = r.End
		}
	}
	return rainNow, haveNow, until, spells
}

func faceDays(obs weatherObservation, now time.Time) (*weatherDay, []weatherDay) {
	today := -1
	for i, d := range obs.Days {
		if !d.Start.After(now) && now.Before(dayEnd(d)) {
			today = i
		}
	}
	if today < 0 {
		return nil, nil
	}
	t := obs.Days[today]
	t.MinC = minOpt(t.MinC, &obs.TempC)
	t.MaxC = maxOpt(t.MaxC, &obs.TempC)
	next := obs.Days[today+1:]
	return &t, next[:min(len(next), faceForecastDays)]
}
