package main

import (
	"math"
	"sort"
	"time"
)

const deg2rad = math.Pi / 180

const (
	sunHorizonDeg        = -0.833
	sunCrossingTolerance = 0.5 / 86400
	sunCrossingMaxSteps  = 60
	sunExtremeQuarter    = 0.25
	sunExtremeMargin     = 1e-3
	sunPolarCosLat       = 0.002
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

func solarTransit(n, lon float64) float64 {
	jStar := n - lon/360.0
	mRad, lRad := sunAnomalyLongitude(jStar)
	return 2451545.0 + jStar + 0.0053*math.Sin(mRad) - 0.0069*math.Sin(2*lRad)
}

type sunCrossings struct {
	Rise, Set         time.Time
	HaveRise, HaveSet bool
}

func (c sunCrossings) both() bool { return c.HaveRise && c.HaveSet }

func (c sunCrossings) either() bool { return c.HaveRise || c.HaveSet }

func sunTimes(lat, lon float64, date time.Time) sunCrossings {
	return sunAt(lat, lon, date, sunHorizonDeg)
}

func sunAt(lat, lon float64, date time.Time, altDeg float64) sunCrossings {
	n := math.Round(julianDate(date) - 2451545.0 + 0.0008)
	prev, transit, next := solarTransit(n-1, lon), solarTransit(n, lon), solarTransit(n+1, lon)
	sinLat, cosLat := math.Sincos(lat * deg2rad)
	sinAlt := math.Sin(altDeg * deg2rad)
	f := func(j float64) float64 { return sunHeight(sinLat, cosLat, lon, j) - sinAlt }
	polar := cosLat < sunPolarCosLat
	dawnSide := sunExtreme(f, prev+sunExtremeQuarter, transit-sunExtremeQuarter, -1, polar)
	noon := sunExtreme(f, transit-sunExtremeQuarter, transit+sunExtremeQuarter, 1, polar)
	duskSide := sunExtreme(f, transit+sunExtremeQuarter, next-sunExtremeQuarter, -1, polar)
	var c sunCrossings
	c.add(sunCrossing(f, dawnSide, noon))
	c.add(sunCrossing(f, noon, duskSide))
	return c
}

func (c *sunCrossings) add(at time.Time, rise, ok bool) {
	switch {
	case !ok:
	case rise:
		c.Rise, c.HaveRise = at, true
	default:
		c.Set, c.HaveSet = at, true
	}
}

type sunPoint struct{ j, f float64 }

func sunExtreme(f func(float64) float64, from, to, sign float64, always bool) sunPoint {
	p := sunPoint{(from + to) / 2, 0}
	p.f = f(p.j)
	if !always && math.Abs(p.f) > sunExtremeMargin {
		return p
	}
	const invPhi = 0.6180339887498949
	x1, x2 := to-invPhi*(to-from), from+invPhi*(to-from)
	f1, f2 := sign*f(x1), sign*f(x2)
	for to-from > sunCrossingTolerance {
		if f1 > f2 {
			to, x2, f2 = x2, x1, f1
			x1 = to - invPhi*(to-from)
			f1 = sign * f(x1)
		} else {
			from, x1, f1 = x1, x2, f2
			x2 = from + invPhi*(to-from)
			f2 = sign * f(x2)
		}
	}
	m := (from + to) / 2
	return sunPoint{m, f(m)}
}

func sunCrossing(f func(float64) float64, a, b sunPoint) (at time.Time, rise, ok bool) {
	rise = b.f > 0
	if (a.f > 0) == rise {
		return time.Time{}, false, false
	}
	side := 0
	for range sunCrossingMaxSteps {
		if b.j-a.j < sunCrossingTolerance {
			break
		}
		m := sunPoint{j: (a.j*b.f - b.j*a.f) / (b.f - a.f)}
		if m.f = f(m.j); m.f == 0 {
			return julianToTime(m.j), rise, true
		}
		if (m.f > 0) == (b.f > 0) {
			b = m
			if side == -1 {
				a.f /= 2
			}
			side = -1
		} else {
			a = m
			if side == 1 {
				b.f /= 2
			}
			side = 1
		}
	}
	return julianToTime((a.j + b.j) / 2), rise, true
}

func solarElevation(lat, lon float64, now time.Time) float64 {
	sinLat, cosLat := math.Sincos(lat * deg2rad)
	return math.Asin(sunHeight(sinLat, cosLat, lon, float64(now.Unix())/86400+2440587.5)) / deg2rad
}

func sunHeight(sinLat, cosLat, lon, jd float64) float64 {
	n := math.Floor(jd - 2451545.0 + lon/360.0)
	t0, t1 := solarTransit(n, lon), solarTransit(n+1, lon)
	sinDecl, decl := sunDeclination(jd)
	return sinLat*sinDecl + cosLat*math.Cos(decl)*math.Cos((jd-t0)/(t1-t0)*2*math.Pi)
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

func localSunTimes(lat, lon float64, now time.Time, obs weatherObservation) sunCrossings {
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
	civil := sunAt(lat, lon, date, civilTwilightDeg)
	blue := sunAt(lat, lon, date, blueHourEndDeg)
	golden := sunAt(lat, lon, date, goldenHourEndDeg)
	return twilight{Dawn: civil.Rise, BlueEnd: blue.Rise, GoldenEnd: golden.Rise,
		GoldenStart: golden.Set, BlueStart: blue.Set, Dusk: civil.Set}
}

type sunEvent struct {
	at   time.Time
	rise bool
}

func sunEvents(lat, lon float64, now time.Time) []sunEvent {
	var evs []sunEvent
	now = now.UTC()
	for d := -1; d <= 1; d++ {
		c := sunTimes(lat, lon, now.AddDate(0, 0, d))
		if c.HaveRise {
			evs = append(evs, sunEvent{c.Rise, true})
		}
		if c.HaveSet {
			evs = append(evs, sunEvent{c.Set, false})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) })
	return evs
}

func sunEventsAround(lat, lon float64, now time.Time) (last, next *sunEvent) {
	return sunBracket(sunEvents(lat, lon, now), now)
}

func sunBracket(evs []sunEvent, now time.Time) (last, next *sunEvent) {
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
