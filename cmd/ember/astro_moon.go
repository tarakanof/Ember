package main

import (
	"math"
	"sort"
	"time"
)

const (
	moonHorizonDeg = 0.125
	moonSearch     = 36 * time.Hour
	moonScanStep   = time.Hour
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

func normDeg(x float64) float64 {
	x = math.Mod(x, 360)
	if x < 0 {
		x += 360
	}
	return x
}

func moonEcliptic(t time.Time) (lambda, beta float64) {
	T := julianCenturies(t)
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
	return normDeg(lp + sl/1e6), sb / 1e6
}

func sunEclipticLongitude(t time.Time) float64 {
	T := julianCenturies(t)
	l0 := 280.46646 + 36000.76983*T
	m := (357.52911 + 35999.05029*T) * deg2rad
	c := (1.914602-0.004817*T)*math.Sin(m) + 0.019993*math.Sin(2*m) + 0.000289*math.Sin(3*m)
	return normDeg(l0 + c)
}

func moonAltitude(lat, lon float64, t time.Time) float64 {
	lambda, beta := moonEcliptic(t)
	T := julianCenturies(t)
	eps := (23.4392911 - 0.0130042*T) * deg2rad
	l, b := lambda*deg2rad, beta*deg2rad
	ra := math.Atan2(math.Sin(l)*math.Cos(eps)-math.Tan(b)*math.Sin(eps), math.Cos(l))
	dec := math.Asin(math.Sin(b)*math.Cos(eps) + math.Cos(b)*math.Sin(eps)*math.Sin(l))
	gmst := normDeg(280.46061837 + 360.98564736629*T*36525 + 0.000387933*T*T)
	h := (gmst+lon)*deg2rad - ra
	phi := lat * deg2rad
	return math.Asin(math.Sin(phi)*math.Sin(dec)+math.Cos(phi)*math.Cos(dec)*math.Cos(h)) / deg2rad
}

func moonPhaseAt(t time.Time) (phase, illum float64) {
	lambda, beta := moonEcliptic(t)
	diff := normDeg(lambda - sunEclipticLongitude(t))
	cosPsi := math.Cos(beta*deg2rad) * math.Cos(diff*deg2rad)
	return diff / 360, (1 - cosPsi) / 2
}

func moonIllumination(t time.Time) (illum float64, waxing bool) {
	phase, illum := moonPhaseAt(t)
	return illum, phase < 0.5
}

type moonEvent struct {
	at   time.Time
	rise bool
}

func moonEvents(lat, lon float64, from, to time.Time) []moonEvent {
	above := func(t time.Time) bool { return moonAltitude(lat, lon, t) > moonHorizonDeg }
	var evs []moonEvent
	prev := from.Truncate(moonScanStep)
	prevUp := above(prev)
	for g := prev.Add(moonScanStep); !prev.After(to); g = g.Add(moonScanStep) {
		up := above(g)
		if up != prevUp {
			lo, hi := prev, g
			for hi.Sub(lo) > time.Second {
				mid := lo.Add(hi.Sub(lo) / 2)
				if above(mid) == prevUp {
					lo = mid
				} else {
					hi = mid
				}
			}
			evs = append(evs, moonEvent{at: hi.Round(time.Minute), rise: up})
		}
		prev, prevUp = g, up
	}
	return evs
}

func moonUpWindow(lat, lon float64, now time.Time) (rise, set time.Time, up bool) {
	evs := moonEvents(lat, lon, now.Add(-moonSearch), now.Add(moonSearch))
	next := sort.Search(len(evs), func(i int) bool { return evs[i].at.After(now) })
	up = moonAltitude(lat, lon, now) > moonHorizonDeg
	if next > 0 {
		up = evs[next-1].rise
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
