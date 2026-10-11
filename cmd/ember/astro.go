package main

import (
	"math"
	"sort"
	"time"
)

const deg2rad = math.Pi / 180

const sunHorizonDeg = -0.833

func solarTransit(n, lon float64) (jTransit, sinDecl, decl float64) {
	jStar := n - lon/360.0
	m := math.Mod(357.5291+0.98560028*jStar, 360)
	mRad := m * deg2rad
	c := 1.9148*math.Sin(mRad) + 0.0200*math.Sin(2*mRad) + 0.0003*math.Sin(3*mRad)
	lambda := math.Mod(m+c+180+102.9372, 360)
	lRad := lambda * deg2rad

	jTransit = 2451545.0 + jStar + 0.0053*math.Sin(mRad) - 0.0069*math.Sin(2*lRad)
	sinDecl = math.Sin(lRad) * math.Sin(23.4397*deg2rad)
	return jTransit, sinDecl, math.Asin(sinDecl)
}

func sunTimes(lat, lon float64, date time.Time) (sunrise, sunset time.Time, ok bool) {
	return sunAt(lat, lon, date, sunHorizonDeg)
}

func sunAt(lat, lon float64, date time.Time, altDeg float64) (rising, setting time.Time, ok bool) {
	jd := julianDate(date)
	n := math.Round(jd - 2451545.0 + 0.0008)
	jTransit, sinDecl, decl := solarTransit(n, lon)

	cosOmega := (math.Sin(altDeg*deg2rad) - math.Sin(lat*deg2rad)*sinDecl) /
		(math.Cos(lat*deg2rad) * math.Cos(decl))
	if cosOmega > 1 || cosOmega < -1 {
		return time.Time{}, time.Time{}, false
	}
	omega := math.Acos(cosOmega) / deg2rad

	jRise := jTransit - omega/360.0
	jSet := jTransit + omega/360.0
	return julianToTime(jRise), julianToTime(jSet), true
}

func solarElevation(lat, lon float64, now time.Time) float64 {
	jd := float64(now.Unix())/86400 + 2440587.5
	n := math.Round(jd - 2451545.0 + lon/360.0)
	jTransit, sinDecl, decl := solarTransit(n, lon)
	hourAngle := (jd - jTransit) * 2 * math.Pi
	sinAlt := math.Sin(lat*deg2rad)*sinDecl + math.Cos(lat*deg2rad)*math.Cos(decl)*math.Cos(hourAngle)
	return math.Asin(sinAlt) / deg2rad
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

func localSolarDate(now time.Time, obs weatherObservation, lon float64) time.Time {
	return localNoon(now, obs, lon).Add(time.Duration(lon / 15 * float64(time.Hour)))
}

func localSunTimes(lat, lon float64, now time.Time, obs weatherObservation) (sunrise, sunset time.Time, ok bool) {
	return sunTimes(lat, lon, localSolarDate(now, obs, lon))
}

const (
	civilTwilightDeg = -6
	blueHourEndDeg   = -4
	goldenHourEndDeg = 6
)

type twilight struct {
	Dawn, BlueEnd, GoldenEnd, GoldenStart, BlueStart, Dusk time.Time
}

func localTwilight(lat, lon float64, now time.Time, obs weatherObservation) twilight {
	date := localSolarDate(now, obs, lon)
	var tw twilight
	tw.Dawn, tw.Dusk, _ = sunAt(lat, lon, date, civilTwilightDeg)
	tw.BlueEnd, tw.BlueStart, _ = sunAt(lat, lon, date, blueHourEndDeg)
	tw.GoldenEnd, tw.GoldenStart, _ = sunAt(lat, lon, date, goldenHourEndDeg)
	return tw
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
	return nightBetween(last, next, lat, lon, now)
}

func nightBetween(last, next *sunEvent, lat, lon float64, now time.Time) bool {
	if last != nil && next != nil {
		return !last.rise
	}
	return solarElevation(lat, lon, now) < sunHorizonDeg
}
