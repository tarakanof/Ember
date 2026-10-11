package main

import (
	"math"
	"sort"
	"time"
)

const deg2rad = math.Pi / 180

const (
	sunHorizonDeg  = -0.833
	sunRefineSteps = 5
)

func sunAnomalyLongitude(days float64) (mRad, lRad float64) {
	m := math.Mod(357.5291+0.98560028*days, 360)
	mRad = m * deg2rad
	c := 1.9148*math.Sin(mRad) + 0.0200*math.Sin(2*mRad) + 0.0003*math.Sin(3*mRad)
	return mRad, math.Mod(m+c+180+102.9372+1.71946*days/36525, 360) * deg2rad
}

func sunDeclination(jd float64) (sinDecl, decl float64) {
	_, lRad := sunAnomalyLongitude(jd - 2451545.0)
	sinDecl = math.Sin(lRad) * math.Sin(23.4397*deg2rad)
	return sinDecl, math.Asin(sinDecl)
}

func solarTransit(n, lon float64) (jTransit, sinDecl, decl float64) {
	jStar := n - lon/360.0
	mRad, lRad := sunAnomalyLongitude(jStar)
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
	halfDay := func(sinDecl, decl float64) (float64, bool) {
		cosOmega := (math.Sin(altDeg*deg2rad) - math.Sin(lat*deg2rad)*sinDecl) /
			(math.Cos(lat*deg2rad) * math.Cos(decl))
		if cosOmega > 1 || cosOmega < -1 {
			return 0, false
		}
		return math.Acos(cosOmega) / deg2rad / 360, true
	}
	omega, ok := halfDay(sinDecl, decl)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	at := func(sign float64) time.Time {
		j := jTransit + sign*omega
		for range sunRefineSteps {
			o, ok := halfDay(sunDeclination(j))
			if !ok {
				return time.Time{}
			}
			next := jTransit + sign*o
			if math.Abs(next-j) < 1.0/86400 {
				return julianToTime(next)
			}
			j = next
		}
		return julianToTime(j)
	}
	rising, setting = at(-1), at(1)
	return rising, setting, !rising.IsZero() && !setting.IsZero()
}

func solarElevation(lat, lon float64, now time.Time) float64 {
	jd := float64(now.Unix())/86400 + 2440587.5
	n := math.Round(jd - 2451545.0 + lon/360.0)
	jTransit, _, _ := solarTransit(n, lon)
	sinDecl, decl := sunDeclination(jd)
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
		rise, set, _ := sunTimes(lat, lon, now.AddDate(0, 0, d))
		if !rise.IsZero() {
			evs = append(evs, sunEvent{rise, true})
		}
		if !set.IsZero() {
			evs = append(evs, sunEvent{set, false})
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
