package main

import (
	"math"
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

func localClock(t time.Time, lon float64) string {
	offset := time.Duration(math.Round(lon/15.0)) * time.Hour
	return t.Add(offset).UTC().Format("15:04")
}

func isNight(lat, lon float64, now time.Time) bool {
	sunrise, sunset, ok := sunTimes(lat, lon, now)
	if !ok {
		return false
	}
	return now.Before(sunrise) || now.After(sunset)
}
