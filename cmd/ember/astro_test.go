package main

import (
	"math"
	"testing"
	"time"
)

func afterDays(t time.Time, days float64) time.Time {
	return t.Add(time.Duration(days * 24 * float64(time.Hour)))
}

func TestMoonIlluminationKnownPhases(t *testing.T) {
	if illum, _ := moonIllumination(knownNewMoon); illum > 0.02 {
		t.Errorf("new moon illum = %.3f, want ~0", illum)
	}
	if illum, _ := moonIllumination(afterDays(knownNewMoon, synodicMonth/2)); illum < 0.98 {
		t.Errorf("full moon illum = %.3f, want ~1", illum)
	}
	illum, waxing := moonIllumination(afterDays(knownNewMoon, synodicMonth/4))
	if math.Abs(illum-0.5) > 0.05 || !waxing {
		t.Errorf("first quarter = (%.3f, waxing=%v), want ~0.5 waxing", illum, waxing)
	}
	illum, waxing = moonIllumination(afterDays(knownNewMoon, synodicMonth*3/4))
	if math.Abs(illum-0.5) > 0.05 || waxing {
		t.Errorf("last quarter = (%.3f, waxing=%v), want ~0.5 waning", illum, waxing)
	}
}

func TestSunTimesEquatorEquinox(t *testing.T) {
	date := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	sunrise, sunset, ok := sunTimes(0, 0, date)
	if !ok {
		t.Fatal("equator equinox should have sunrise/sunset")
	}
	if sunrise.After(sunset) {
		t.Error("sunrise after sunset")
	}
	if h := sunrise.UTC().Hour(); h < 5 || h > 6 {
		t.Errorf("equinox sunrise hour = %d, want ~6", h)
	}
	if h := sunset.UTC().Hour(); h < 17 || h > 18 {
		t.Errorf("equinox sunset hour = %d, want ~18", h)
	}
	if y, m, d := sunrise.UTC().Date(); y != 2024 || m != 3 || d != 20 {
		t.Errorf("sunrise date = %v-%v-%v, want 2024-3-20", y, m, d)
	}
}

func TestSunTimesPolar(t *testing.T) {
	if _, _, ok := sunTimes(80, 0, time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("polar day should have no sunrise/sunset")
	}
	if _, _, ok := sunTimes(80, 0, time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("polar night should have no sunrise/sunset")
	}
}

func TestSunClockUsesKnownOffsetElseLongitude(t *testing.T) {
	noonUTC := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	known := weatherObservation{TZKnown: true, TZOffsetSeconds: 7200}
	cases := []struct {
		obs  weatherObservation
		lon  float64
		want string
	}{
		{known, 20.479, "14:00"},
		{weatherObservation{}, 20.479, "13:00"},
		{weatherObservation{}, 15, "13:00"},
		{weatherObservation{}, -30, "10:00"},
	}
	for _, c := range cases {
		if got := sunClock(noonUTC, c.obs, c.lon); got != c.want {
			t.Errorf("sunClock(known=%v, lon %v) = %q, want %s", c.obs.TZKnown, c.lon, got, c.want)
		}
	}
}
