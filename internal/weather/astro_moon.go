package weather

import (
	"math"
	"sort"
	"time"
)

const (
	moonSearch         = 36 * time.Hour
	moonScanStep       = time.Hour
	moonNearHorizonDeg = 1.0
	moonTurnPrecision  = 20 * time.Second
	deltaT             = 69 * time.Second
)

type lunarTerm struct {
	d, m, mp, f float64
	coef        float64
}

var moonLongitudeTerms = []lunarTerm{
	{0, 0, 1, 0, 6288774}, {2, 0, -1, 0, 1274027}, {2, 0, 0, 0, 658314}, {0, 0, 2, 0, 213618},
	{0, 1, 0, 0, -185116}, {0, 0, 0, 2, -114332}, {2, 0, -2, 0, 58793}, {2, -1, -1, 0, 57066},
	{2, 0, 1, 0, 53322}, {2, -1, 0, 0, 45758}, {0, 1, -1, 0, -40923}, {1, 0, 0, 0, -34720},
	{0, 1, 1, 0, -30383}, {2, 0, 0, -2, 15327}, {0, 0, 1, 2, -12528}, {0, 0, 1, -2, 10980},
	{4, 0, -1, 0, 10675}, {0, 0, 3, 0, 10034}, {4, 0, -2, 0, 8548}, {2, 1, -1, 0, -7888},
	{2, 1, 0, 0, -6766}, {1, 0, -1, 0, -5163}, {1, 1, 0, 0, 4987}, {2, -1, 1, 0, 4036},
	{2, 0, 2, 0, 3994}, {4, 0, 0, 0, 3861}, {2, 0, -3, 0, 3665}, {0, 1, -2, 0, -2689},
	{2, 0, -1, 2, -2602}, {2, -1, -2, 0, 2390}, {1, 0, 1, 0, -2348}, {2, -2, 0, 0, 2236},
}

var moonDistanceTerms = []lunarTerm{
	{0, 0, 1, 0, -20905355}, {2, 0, -1, 0, -3699111}, {2, 0, 0, 0, -2955968}, {0, 0, 2, 0, -569925},
	{0, 1, 0, 0, 48888}, {0, 0, 0, 2, -3149}, {2, 0, -2, 0, 246158}, {2, -1, -1, 0, -152138},
	{2, 0, 1, 0, -170733}, {2, -1, 0, 0, -204586}, {0, 1, -1, 0, -129620}, {1, 0, 0, 0, 108743},
	{0, 1, 1, 0, 104755}, {2, 0, 0, -2, 10321}, {0, 0, 1, -2, 79661}, {4, 0, -1, 0, -34782},
	{0, 0, 3, 0, -23210}, {4, 0, -2, 0, -21636}, {2, 1, -1, 0, 24208}, {2, 1, 0, 0, 30824},
	{1, 0, -1, 0, -8379}, {1, 1, 0, 0, -16675}, {2, -1, 1, 0, -12831}, {2, 0, 2, 0, -10445},
	{4, 0, 0, 0, -11650}, {2, 0, -3, 0, 14403}, {0, 1, -2, 0, -7003}, {2, -1, -2, 0, 10056},
	{1, 0, 1, 0, 6322}, {2, -2, 0, 0, -9884},
}

var moonLatitudeTerms = []lunarTerm{
	{0, 0, 0, 1, 5128122}, {0, 0, 1, 1, 280602}, {0, 0, 1, -1, 277693}, {2, 0, 0, -1, 173237},
	{2, 0, -1, 1, 55413}, {2, 0, -1, -1, 46271}, {2, 0, 0, 1, 32573}, {0, 0, 2, 1, 17198},
	{2, 0, 1, -1, 9266}, {0, 0, 2, -1, 8822}, {2, -1, 0, -1, 8216}, {2, 0, -2, -1, 4324},
	{2, 0, 1, 1, 4200}, {2, 1, 0, -1, -3359}, {2, -1, -1, 1, 2463}, {2, -1, 0, 1, 2211},
}

func julianCenturies(t time.Time) float64 {
	jd := float64(t.Unix())/86400 + 2440587.5
	return (jd - 2451545.0) / 36525
}

func ephemerisCenturies(t time.Time) float64 {
	return julianCenturies(t.Add(deltaT))
}

func normDeg(x float64) float64 {
	x = math.Mod(x, 360)
	if x < 0 {
		x += 360
	}
	return x
}

func moonEcliptic(t time.Time) (lambda, beta, distKm float64) {
	T := ephemerisCenturies(t)
	lp := normDeg(218.3164477 + 481267.88123421*T)
	d := normDeg(297.8501921 + 445267.1114034*T)
	m := normDeg(357.5291092 + 35999.0502909*T)
	mp := normDeg(134.9633964 + 477198.8675055*T)
	f := normDeg(93.2720950 + 483202.0175233*T)
	e := 1 - 0.002516*T - 0.0000074*T*T
	a1 := normDeg(119.75 + 131.849*T)
	a2 := normDeg(53.09 + 479264.290*T)
	a3 := normDeg(313.45 + 481266.484*T)
	sum := func(terms []lunarTerm, fn func(float64) float64) float64 {
		var s float64
		for _, k := range terms {
			c := k.coef * math.Pow(e, math.Abs(k.m))
			s += c * fn((k.d*d+k.m*m+k.mp*mp+k.f*f)*deg2rad)
		}
		return s
	}
	sl := sum(moonLongitudeTerms, math.Sin) + 3958*math.Sin(a1*deg2rad) + 1962*math.Sin((lp-f)*deg2rad) + 318*math.Sin(a2*deg2rad)
	sb := sum(moonLatitudeTerms, math.Sin) - 2235*math.Sin(lp*deg2rad) + 382*math.Sin(a3*deg2rad) +
		175*math.Sin((a1-f)*deg2rad) + 175*math.Sin((a1+f)*deg2rad) + 127*math.Sin((lp-mp)*deg2rad) - 115*math.Sin((lp+mp)*deg2rad)
	return normDeg(lp + sl/1e6), sb / 1e6, 385000.56 + sum(moonDistanceTerms, math.Cos)/1000
}

const earthRadiusKm = 6378.14

func moonHorizon(distKm float64) float64 {
	return 0.7275*math.Asin(earthRadiusKm/distKm)/deg2rad - 0.5667
}

func sunEclipticLongitude(t time.Time) float64 {
	T := ephemerisCenturies(t)
	l0 := 280.46646 + 36000.76983*T
	m := (357.52911 + 35999.05029*T) * deg2rad
	c := (1.914602-0.004817*T)*math.Sin(m) + 0.019993*math.Sin(2*m) + 0.000289*math.Sin(3*m)
	return normDeg(l0 + c)
}

func moonAboveHorizon(lat, lon float64, t time.Time) float64 {
	lambda, beta, dist := moonEcliptic(t)
	T := julianCenturies(t)
	eps := (23.4392911 - 0.0130042*T) * deg2rad
	l, b := lambda*deg2rad, beta*deg2rad
	ra := math.Atan2(math.Sin(l)*math.Cos(eps)-math.Tan(b)*math.Sin(eps), math.Cos(l))
	dec := math.Asin(math.Sin(b)*math.Cos(eps) + math.Cos(b)*math.Sin(eps)*math.Sin(l))
	gmst := normDeg(280.46061837 + 360.98564736629*T*36525 + 0.000387933*T*T)
	h := (gmst+lon)*deg2rad - ra
	phi := lat * deg2rad
	return math.Asin(math.Sin(phi)*math.Sin(dec)+math.Cos(phi)*math.Cos(dec)*math.Cos(h))/deg2rad - moonHorizon(dist)
}

func MoonPhaseAt(t time.Time) (phase, illum float64) {
	lambda, beta, _ := moonEcliptic(t)
	diff := normDeg(lambda - sunEclipticLongitude(t))
	cosPsi := math.Cos(beta*deg2rad) * math.Cos(diff*deg2rad)
	return diff / 360, (1 - cosPsi) / 2
}

func MoonIllumination(t time.Time) (illum float64, waxing bool) {
	phase, illum := MoonPhaseAt(t)
	return illum, phase < 0.5
}

type moonEvent struct {
	at   time.Time
	rise bool
}

func moonEvents(lat, lon float64, from, to time.Time) []moonEvent {
	alt := func(t time.Time) float64 { return moonAboveHorizon(lat, lon, t) }
	cross := func(lo, hi time.Time, loUp bool) time.Time {
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if (alt(mid) > 0) == loUp {
				lo = mid
			} else {
				hi = mid
			}
		}
		return hi.Round(time.Minute)
	}
	var evs []moonEvent
	prev := from.Truncate(moonScanStep)
	prevAlt := alt(prev)
	for g := prev.Add(moonScanStep); !prev.After(to); g = g.Add(moonScanStep) {
		a := alt(g)
		prevUp, up := prevAlt > 0, a > 0
		switch {
		case up != prevUp:
			evs = append(evs, moonEvent{at: cross(prev, g, prevUp), rise: up})
		case min(math.Abs(prevAlt), math.Abs(a)) < moonNearHorizonDeg:
			if m := moonTurn(alt, prev, g, up); (alt(m) > 0) != up {
				evs = append(evs, moonEvent{at: cross(prev, m, up), rise: !up}, moonEvent{at: cross(m, g, !up), rise: up})
			}
		}
		prev, prevAlt = g, a
	}
	return evs
}

func moonTurn(alt func(time.Time) float64, lo, hi time.Time, up bool) time.Time {
	f := func(t time.Time) float64 {
		if up {
			return -alt(t)
		}
		return alt(t)
	}
	const r = 0.6180339887498949
	span := hi.Sub(lo)
	a, b := lo.Add(time.Duration(float64(span)*(1-r))), lo.Add(time.Duration(float64(span)*r))
	fa, fb := f(a), f(b)
	for hi.Sub(lo) > moonTurnPrecision {
		if fa < fb {
			lo, a, fa = a, b, fb
			b = lo.Add(time.Duration(float64(hi.Sub(lo)) * r))
			fb = f(b)
		} else {
			hi, b, fb = b, a, fa
			a = hi.Add(-time.Duration(float64(hi.Sub(lo)) * r))
			fa = f(a)
		}
	}
	if fa > fb {
		return a
	}
	return b
}

func MoonUpWindow(lat, lon float64, now time.Time) (rise, set time.Time, up bool) {
	evs := moonEvents(lat, lon, now.Add(-moonSearch), now.Add(moonSearch))
	next := sort.Search(len(evs), func(i int) bool { return evs[i].at.After(now) })
	if next > 0 {
		up = evs[next-1].rise
	} else {
		up = moonAboveHorizon(lat, lon, now) > 0
	}
	if up {
		if next > 0 {
			rise = evs[next-1].at
		}
		if next < len(evs) {
			set = evs[next].at
		}
		return rise, set, up
	}
	if next < len(evs) {
		rise = evs[next].at
	}
	if next+1 < len(evs) {
		set = evs[next+1].at
	}
	return rise, set, up
}
