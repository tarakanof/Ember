package weather

import (
	"math"
	"sort"
	"time"
)

const deg2rad = math.Pi / 180

const (
	SunHorizonDeg        = -0.833
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

type SunCrossings struct {
	Rise, Set         time.Time
	HaveRise, HaveSet bool
}

func (c SunCrossings) Both() bool { return c.HaveRise && c.HaveSet }

func (c SunCrossings) Either() bool { return c.HaveRise || c.HaveSet }

func SunTimes(lat, lon float64, date time.Time) SunCrossings {
	return sunAt(lat, lon, date, SunHorizonDeg)
}

func sunAt(lat, lon float64, date time.Time, altDeg float64) SunCrossings {
	n := math.Round(julianDate(date) - 2451545.0 + 0.0008)
	prev, transit, next := solarTransit(n-1, lon), solarTransit(n, lon), solarTransit(n+1, lon)
	sinLat, cosLat := math.Sincos(lat * deg2rad)
	sinAlt := math.Sin(altDeg * deg2rad)
	f := func(j float64) float64 { return sunHeight(sinLat, cosLat, lon, j) - sinAlt }
	polar := cosLat < sunPolarCosLat
	dawnSide := sunExtreme(f, prev+sunExtremeQuarter, transit-sunExtremeQuarter, -1, polar)
	noon := sunExtreme(f, transit-sunExtremeQuarter, transit+sunExtremeQuarter, 1, polar)
	duskSide := sunExtreme(f, transit+sunExtremeQuarter, next-sunExtremeQuarter, -1, polar)
	var c SunCrossings
	c.add(sunCrossing(f, dawnSide, noon))
	c.add(sunCrossing(f, noon, duskSide))
	return c
}

func (c *SunCrossings) add(at time.Time, rise, ok bool) {
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

func SolarElevation(lat, lon float64, now time.Time) float64 {
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

func SunClock(event time.Time, obs Observation, lon float64) string {
	return event.UTC().Add(LocalOffset(obs, lon)).Format("15:04")
}

func LocalOffset(obs Observation, lon float64) time.Duration {
	if obs.TZKnown {
		return time.Duration(obs.TZOffsetSeconds) * time.Second
	}
	return time.Duration(math.Round(lon/15)) * time.Hour
}

func LocalDay(now time.Time, obs Observation, lon float64) string {
	return now.UTC().Add(LocalOffset(obs, lon)).Format("2006-01-02")
}

func localNoon(now time.Time, obs Observation, lon float64) time.Time {
	off := LocalOffset(obs, lon)
	y, m, d := now.UTC().Add(off).Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Add(-off)
}

func localSolarDate(now time.Time, obs Observation, lon float64) time.Time {
	return localNoon(now, obs, lon).Add(time.Duration(lon / 15 * float64(time.Hour)))
}

func LocalSunTimes(lat, lon float64, now time.Time, obs Observation) SunCrossings {
	return SunTimes(lat, lon, localSolarDate(now, obs, lon))
}

const (
	civilTwilightDeg = -6
	blueHourEndDeg   = -4
	goldenHourEndDeg = 6
)

type Twilight struct {
	Dawn, BlueEnd, GoldenEnd, GoldenStart, BlueStart, Dusk time.Time
}

func LocalTwilight(lat, lon float64, now time.Time, obs Observation) Twilight {
	date := localSolarDate(now, obs, lon)
	civil := sunAt(lat, lon, date, civilTwilightDeg)
	blue := sunAt(lat, lon, date, blueHourEndDeg)
	golden := sunAt(lat, lon, date, goldenHourEndDeg)
	return Twilight{Dawn: civil.Rise, BlueEnd: blue.Rise, GoldenEnd: golden.Rise,
		GoldenStart: golden.Set, BlueStart: blue.Set, Dusk: civil.Set}
}

type SunEvent struct {
	At   time.Time
	Rise bool
}

func SunEvents(lat, lon float64, now time.Time) []SunEvent {
	var evs []SunEvent
	now = now.UTC()
	for d := -1; d <= 1; d++ {
		c := SunTimes(lat, lon, now.AddDate(0, 0, d))
		if c.HaveRise {
			evs = append(evs, SunEvent{c.Rise, true})
		}
		if c.HaveSet {
			evs = append(evs, SunEvent{c.Set, false})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
	return evs
}

func SunEventsAround(lat, lon float64, now time.Time) (last, next *SunEvent) {
	return SunBracket(SunEvents(lat, lon, now), now)
}

func SunBracket(evs []SunEvent, now time.Time) (last, next *SunEvent) {
	for i := range evs {
		if !evs[i].At.After(now) {
			last = &evs[i]
		} else if next == nil {
			next = &evs[i]
		}
	}
	return last, next
}

func SunNight(lat, lon float64, now time.Time) bool {
	last, next := SunEventsAround(lat, lon, now)
	return NightBetween(last, next, lat, lon, now)
}

func NightBetween(last, next *SunEvent, lat, lon float64, now time.Time) bool {
	if last != nil && next != nil {
		return !last.Rise
	}
	return SolarElevation(lat, lon, now) < SunHorizonDeg
}

func (c SunCrossings) Pair() (rise, set time.Time, ok bool) { return c.Rise, c.Set, c.Both() }
