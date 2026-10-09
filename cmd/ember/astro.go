package main

import (
	"math"
	"sort"
	"time"
)

const deg2rad = math.Pi / 180

const synodicMonth = 29.530588853

var knownNewMoon = time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)

func moonIllumination(t time.Time) (illum float64, waxing bool) {
	days := t.UTC().Sub(knownNewMoon).Hours() / 24.0
	phase := math.Mod(days, synodicMonth)
	if phase < 0 {
		phase += synodicMonth
	}
	frac := phase / synodicMonth
	illum = (1 - math.Cos(2*math.Pi*frac)) / 2
	return illum, frac < 0.5
}

func sunTimes(lat, lon float64, date time.Time) (sunrise, sunset time.Time, ok bool) {
	jd := julianDate(date)
	n := math.Round(jd - 2451545.0 + 0.0008)

	jStar := n - lon/360.0
	m := math.Mod(357.5291+0.98560028*jStar, 360)
	mRad := m * deg2rad
	c := 1.9148*math.Sin(mRad) + 0.0200*math.Sin(2*mRad) + 0.0003*math.Sin(3*mRad)
	lambda := math.Mod(m+c+180+102.9372, 360)
	lRad := lambda * deg2rad

	jTransit := 2451545.0 + jStar + 0.0053*math.Sin(mRad) - 0.0069*math.Sin(2*lRad)
	sinDecl := math.Sin(lRad) * math.Sin(23.4397*deg2rad)
	decl := math.Asin(sinDecl)

	cosOmega := (math.Sin(-0.833*deg2rad) - math.Sin(lat*deg2rad)*sinDecl) /
		(math.Cos(lat*deg2rad) * math.Cos(decl))
	if cosOmega > 1 || cosOmega < -1 {
		return time.Time{}, time.Time{}, false
	}
	omega := math.Acos(cosOmega) / deg2rad

	jRise := jTransit - omega/360.0
	jSet := jTransit + omega/360.0
	return julianToTime(jRise), julianToTime(jSet), true
}

func julianDate(date time.Time) float64 {
	y, mo, d := date.UTC().Date()
	a := (14 - int(mo)) / 12
	yy := y + 4800 - a
	mm := int(mo) + 12*a - 3
	jdn := d + (153*mm+2)/5 + 365*yy + yy/4 - yy/100 + yy/400 - 32045
	return float64(jdn) - 0.5
}

func julianToTime(jd float64) time.Time {
	unixSeconds := (jd - 2440587.5) * 86400.0
	return time.Unix(int64(math.Round(unixSeconds)), 0).UTC()
}

func sunClock(event time.Time, obs weatherObservation, lon float64) string {
	return event.UTC().Add(localOffset(obs, lon)).Format("15:04")
}

func localOffset(obs weatherObservation, lon float64) time.Duration {
	if obs.TZKnown {
		return time.Duration(obs.TZOffsetSeconds) * time.Second
	}
	return time.Duration(math.Round(lon/15)) * time.Hour
}

func localDay(now time.Time, obs weatherObservation, lon float64) string {
	return now.UTC().Add(localOffset(obs, lon)).Format("2006-01-02")
}

func localNoon(now time.Time, obs weatherObservation, lon float64) time.Time {
	off := localOffset(obs, lon)
	y, m, d := now.UTC().Add(off).Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Add(-off)
}

func localSunTimes(lat, lon float64, now time.Time, obs weatherObservation) (sunrise, sunset time.Time, ok bool) {
	solarDate := localNoon(now, obs, lon).Add(time.Duration(lon / 15 * float64(time.Hour)))
	return sunTimes(lat, lon, solarDate)
}

type sunEvent struct {
	at   time.Time
	rise bool
}

func sunEventsAround(lat, lon float64, now time.Time) (last, next *sunEvent) {
	var evs []sunEvent
	for d := -1; d <= 1; d++ {
		if rise, set, ok := sunTimes(lat, lon, now.AddDate(0, 0, d)); ok {
			evs = append(evs, sunEvent{rise, true}, sunEvent{set, false})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) })
	for i := range evs {
		if !evs[i].at.After(now) {
			last = &evs[i]
		} else if next == nil {
			next = &evs[i]
		}
	}
	return last, next
}

func sunNight(lat, lon float64, now time.Time) bool {
	last, next := sunEventsAround(lat, lon, now)
	if last == nil || next == nil {
		return polarNight(lat, now)
	}
	return !last.rise
}
