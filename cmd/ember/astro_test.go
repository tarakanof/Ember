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
	// At the reference new moon, illumination ≈ 0.
	if illum, _ := moonIllumination(knownNewMoon); illum > 0.02 {
		t.Errorf("new moon illum = %.3f, want ~0", illum)
	}
	// Half a synodic month later → full moon (~1).
	if illum, _ := moonIllumination(afterDays(knownNewMoon, synodicMonth/2)); illum < 0.98 {
		t.Errorf("full moon illum = %.3f, want ~1", illum)
	}
	// First quarter (¼ month): ~half lit and waxing.
	illum, waxing := moonIllumination(afterDays(knownNewMoon, synodicMonth/4))
	if math.Abs(illum-0.5) > 0.05 || !waxing {
		t.Errorf("first quarter = (%.3f, waxing=%v), want ~0.5 waxing", illum, waxing)
	}
	// Last quarter (¾ month): ~half lit and waning.
	illum, waxing = moonIllumination(afterDays(knownNewMoon, synodicMonth*3/4))
	if math.Abs(illum-0.5) > 0.05 || waxing {
		t.Errorf("last quarter = (%.3f, waxing=%v), want ~0.5 waning", illum, waxing)
	}
}

func TestSunTimesEquatorEquinox(t *testing.T) {
	// At (0,0) on the equinox, sunrise ≈ 06:00 and sunset ≈ 18:00 UTC.
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
	// Event falls on the requested calendar day.
	if y, m, d := sunrise.UTC().Date(); y != 2024 || m != 3 || d != 20 {
		t.Errorf("sunrise date = %v-%v-%v, want 2024-3-20", y, m, d)
	}
}

func TestSunTimesPolar(t *testing.T) {
	// High Arctic: polar day in June, polar night in December → no events.
	if _, _, ok := sunTimes(80, 0, time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("polar day should have no sunrise/sunset")
	}
	if _, _, ok := sunTimes(80, 0, time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("polar night should have no sunrise/sunset")
	}
}

func TestLocalClockOffset(t *testing.T) {
	noonUTC := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	if got := localClock(noonUTC, 15); got != "13:00" {
		t.Errorf("lon +15 → %q, want 13:00", got)
	}
	if got := localClock(noonUTC, -30); got != "10:00" {
		t.Errorf("lon -30 → %q, want 10:00", got)
	}
}

func TestSunClockUsesKnownOffset(t *testing.T) {
	noonUTC := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	cfg := WeatherConfig{Longitude: 20.479}
	// Known offset (CEST = +2h) → exact civil time, NOT the longitude approx (+1h).
	if got := sunClock(noonUTC, cfg, true, 7200); got != "14:00" {
		t.Errorf("known-offset clock = %q, want 14:00", got)
	}
	// Unknown offset → longitude approximation (round(20.479/15)=1h).
	if got := sunClock(noonUTC, cfg, false, 0); got != "13:00" {
		t.Errorf("fallback clock = %q, want 13:00", got)
	}
}
