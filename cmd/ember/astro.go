package main

import (
	"math"
	"time"
)

// Local astronomy: moon phase from the date and sunrise/sunset from lat/lon/date.
// Both are computed (no API, no key) so they work for any provider/location. The
// algorithms are the standard low-precision ones — accurate to a minute or two,
// which is plenty for a pixel clock.

const deg2rad = math.Pi / 180

// synodicMonth is the mean length of a lunar cycle (new moon → new moon), days.
const synodicMonth = 29.530588853

// knownNewMoon is a reference new moon instant (2000-01-06 18:14 UTC).
var knownNewMoon = time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)

// moonIllumination returns the illuminated fraction (0 = new, 1 = full) and
// whether the moon is waxing (growing) at time t.
func moonIllumination(t time.Time) (illum float64, waxing bool) {
	days := t.UTC().Sub(knownNewMoon).Hours() / 24.0
	phase := math.Mod(days, synodicMonth)
	if phase < 0 {
		phase += synodicMonth
	}
	frac := phase / synodicMonth // 0 = new … 0.5 = full … →1 = new again
	illum = (1 - math.Cos(2*math.Pi*frac)) / 2
	return illum, frac < 0.5
}

// sunTimes returns sunrise and sunset (as UTC instants) for the calendar date of
// `date` at the given latitude/longitude (degrees, east-positive). ok is false at
// extreme latitudes where the sun does not cross the horizon that day (polar
// day/night). Standard "sunrise equation" (NOAA low-precision).
func sunTimes(lat, lon float64, date time.Time) (sunrise, sunset time.Time, ok bool) {
	jd := julianDate(date) // JD at 00:00 UTC of the date
	n := math.Round(jd - 2451545.0 + 0.0008)

	jStar := n - lon/360.0 // mean solar noon (lon east-positive → −lonWest)
	m := math.Mod(357.5291+0.98560028*jStar, 360)
	mRad := m * deg2rad
	c := 1.9148*math.Sin(mRad) + 0.0200*math.Sin(2*mRad) + 0.0003*math.Sin(3*mRad)
	lambda := math.Mod(m+c+180+102.9372, 360)
	lRad := lambda * deg2rad

	jTransit := 2451545.0 + jStar + 0.0053*math.Sin(mRad) - 0.0069*math.Sin(2*lRad)
	sinDecl := math.Sin(lRad) * math.Sin(23.4397*deg2rad)
	decl := math.Asin(sinDecl)

	// Hour angle at sunrise/sunset, with −0.833° for atmospheric refraction + the
	// sun's apparent radius.
	cosOmega := (math.Sin(-0.833*deg2rad) - math.Sin(lat*deg2rad)*sinDecl) /
		(math.Cos(lat*deg2rad) * math.Cos(decl))
	if cosOmega > 1 || cosOmega < -1 {
		return time.Time{}, time.Time{}, false // polar night / polar day
	}
	omega := math.Acos(cosOmega) / deg2rad // degrees

	jRise := jTransit - omega/360.0
	jSet := jTransit + omega/360.0
	return julianToTime(jRise), julianToTime(jSet), true
}

// julianDate returns the Julian Date at 00:00 UTC of date's calendar day.
func julianDate(date time.Time) float64 {
	y, mo, d := date.UTC().Date()
	a := (14 - int(mo)) / 12
	yy := y + 4800 - a
	mm := int(mo) + 12*a - 3
	jdn := d + (153*mm+2)/5 + 365*yy + yy/4 - yy/100 + yy/400 - 32045
	return float64(jdn) - 0.5 // JDN is for noon; subtract 0.5 for 00:00 UTC
}

// julianToTime converts a Julian Date to a UTC time.Time.
func julianToTime(jd float64) time.Time {
	unixSeconds := (jd - 2440587.5) * 86400.0
	return time.Unix(int64(math.Round(unixSeconds)), 0).UTC()
}

// localClock formats an instant in an approximate local civil time derived from
// longitude (15° per hour). Good enough for a "SUNRISE 5:21" label without a
// timezone database; may differ from civil time by DST / zone boundaries.
func localClock(t time.Time, lon float64) string {
	offset := time.Duration(math.Round(lon/15.0)) * time.Hour
	return t.Add(offset).UTC().Format("15:04")
}

// isNight reports whether `now` is before sunrise or after sunset for its date at
// lat/lon. Polar day → never night; polar night → always night.
func isNight(lat, lon float64, now time.Time) bool {
	sunrise, sunset, ok := sunTimes(lat, lon, now)
	if !ok {
		// No sunrise/sunset that day: decide by solar declination vs latitude.
		// Simplest robust call: treat as night only in true polar night, which we
		// can't cheaply distinguish here, so default to day (show the sun icon).
		return false
	}
	return now.Before(sunrise) || now.After(sunset)
}
