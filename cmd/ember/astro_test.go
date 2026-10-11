package main

import (
	"math"
	"testing"
	"time"
)

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

type moonReference struct {
	name       string
	lat, lon   float64
	rise, sets []int64
}

var openMeteoMoon20260901 = []moonReference{
	{"Berlin", 52.512604, 13.419517,
		[]int64{1788288663, 1788376319, 1788464546, 1788553659, 1788644003, 1788735324},
		[]int64{1788255634, 1788347288, 1788438941, 1788530224, 1788620568, 1788709788}},
	{"Oslo", 59.906296, 10.744095,
		[]int64{1788287116, 1788373767, 1788460794, 1788548855, 1788639177, 1788731714},
		[]int64{1788258276, 1788350988, 1788443924, 1788536369, 1788626733, 1788714843}},
	{"Sydney", -33.848858, 151.19551,
		[]int64{1788263579, 1788354118, 1788444734, 1788535295, 1788625490, 1788715245},
		[]int64{1788302040, 1788390817, 1788480046, 1788569845, 1788660164}},
	{"Quito", -0.035149384, -78.50467,
		[]int64{1788228338, 1788317801, 1788407527, 1788497483, 1788587639, 1788677807},
		[]int64{1788272959, 1788362566, 1788452383, 1788542478, 1788632633, 1788722794}},
}

func TestMoonRiseSetMatchOpenMeteoReference(t *testing.T) {
	from, to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	for _, ref := range openMeteoMoon20260901 {
		evs := moonEvents(ref.lat, ref.lon, from, to)
		check := func(want []int64, rise bool) {
			lo, hi := time.Unix(want[0], 0).Add(-2*time.Minute), time.Unix(want[len(want)-1], 0).Add(2*time.Minute)
			n := 0
			for _, e := range evs {
				if e.rise == rise && !e.at.Before(lo) && !e.at.After(hi) {
					n++
				}
			}
			if n != len(want) {
				t.Errorf("%s: %d computed rise=%v events in the reference span, want %d", ref.name, n, rise, len(want))
			}
			for _, w := range want {
				best := time.Duration(math.MaxInt64)
				for _, e := range evs {
					if e.rise == rise {
						best = min(best, e.at.Sub(time.Unix(w, 0)).Abs())
					}
				}
				if best > 70*time.Second {
					t.Errorf("%s: no moon rise=%v within 70 s of %v (closest %v)", ref.name, rise, time.Unix(w, 0).UTC(), best)
				}
			}
		}
		check(ref.rise, true)
		check(ref.sets, false)
	}
}

var metNoMoonOslo202609 = struct {
	lat, lon        float64
	riseMin, setMin []int64
}{59.9139, 10.7522,
	[]int64{29804785, 29806229, 29807680, 29809148, 29810654, 29812196, 29813748, 29815296, 29816839, 29818376,
		29819910, 29821443, 29822976, 29824511, 29826045, 29827575, 29829085, 29830565, 29832023, 29833471,
		29834914, 29836355, 29837796, 29839236, 29840676, 29842116, 29843558, 29845001, 29846450},
	[]int64{29804304, 29805849, 29807398, 29808938, 29810445, 29811913, 29813364, 29814808, 29816250, 29817690,
		29819130, 29820570, 29822011, 29823452, 29824895, 29826341, 29827797, 29829273, 29830780, 29832306,
		29833840, 29835373, 29836904, 29838435, 29839967, 29841501, 29843040, 29844585, 29846135},
}

func TestMoonRiseSetMatchMETNorwayToTheRoundedMinute(t *testing.T) {
	ref := metNoMoonOslo202609
	evs := moonEvents(ref.lat, ref.lon, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	check := func(want []int64, rise bool) {
		for _, m := range want {
			w := time.Unix(m*60, 0)
			best := time.Duration(math.MaxInt64)
			for _, e := range evs {
				if e.rise == rise {
					best = min(best, e.at.Sub(w).Abs())
				}
			}
			if best > time.Minute {
				t.Errorf("Oslo: moon rise=%v at %v per MET Norway, closest computed %v off", rise, w.UTC(), best)
			}
		}
	}
	check(ref.riseMin, true)
	check(ref.setMin, false)
}

func TestMoonPositionMatchesMeeusExample47a(t *testing.T) {
	lambda, beta, dist := moonEcliptic(time.Date(1992, 4, 12, 0, 0, 0, 0, time.UTC).Add(-deltaT))
	if math.Abs(lambda-133.162655) > 0.0005 || math.Abs(beta+3.229126) > 0.005 || math.Abs(dist-368409.7) > 20 {
		t.Errorf("moon at 1992-04-12 0h TD (0h minus deltaT in UT): lambda %.6f beta %.6f dist %.1f km, want 133.162655, -3.229126, 368409.7", lambda, beta, dist)
	}
	if h := moonHorizon(368409.7); math.Abs(h-0.1550) > 0.0005 {
		t.Errorf("rise/set altitude at 368409.7 km = %.4f deg, want 0.7275 pi - 0.5667 = 0.1550", h)
	}
	if h := moonHorizon(404000); math.Abs(h-0.0914) > 0.0005 {
		t.Errorf("rise/set altitude at apogee = %.4f deg, want 0.0914", h)
	}
}

func TestMoonPhaseAtEclipses(t *testing.T) {
	newMoon := time.Date(2026, 8, 12, 17, 46, 0, 0, time.UTC)
	fullMoon := time.Date(2026, 8, 28, 4, 13, 0, 0, time.UTC)
	if phase, illum := moonPhaseAt(newMoon); (phase > 0.005 && phase < 0.995) || illum > 0.002 {
		t.Errorf("solar eclipse: phase %.4f illum %.4f, want new", phase, illum)
	}
	if phase, illum := moonPhaseAt(fullMoon); math.Abs(phase-0.5) > 0.005 || illum < 0.998 {
		t.Errorf("lunar eclipse: phase %.4f illum %.4f, want full", phase, illum)
	}
	if illum, waxing := moonIllumination(newMoon.Add(7 * 24 * time.Hour)); !waxing || math.Abs(illum-0.5) > 0.1 {
		t.Errorf("a week after new: illum %.3f waxing %v, want about half and waxing", illum, waxing)
	}
	if _, waxing := moonIllumination(fullMoon.Add(24 * time.Hour)); waxing {
		t.Error("a day after full reads as waxing")
	}
}

func TestMoonUpWindowIsTheCurrentOrNextInterval(t *testing.T) {
	berlin := openMeteoMoon20260901[0]
	near := func(got time.Time, want int64) bool { return got.Sub(time.Unix(want, 0)).Abs() <= 2*time.Minute }
	up := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	rise, set, isUp := moonUpWindow(berlin.lat, berlin.lon, up)
	if !isUp || !near(rise, berlin.rise[0]) || !near(set, berlin.sets[1]) {
		t.Errorf("moon up: up %v rise %v set %v, want up, the rise before now and the set after", isUp, rise, set)
	}
	down := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	rise, set, isUp = moonUpWindow(berlin.lat, berlin.lon, down)
	if isUp || !near(rise, berlin.rise[1]) || !near(set, berlin.sets[2]) {
		t.Errorf("moon down: up %v rise %v set %v, want down, the next rise and its set", isUp, rise, set)
	}
	r2, s2, _ := moonUpWindow(berlin.lat, berlin.lon, down.Add(7*time.Minute+13*time.Second))
	if !r2.Equal(rise) || !s2.Equal(set) || rise.Second() != 0 {
		t.Errorf("window moved with the clock: %v-%v then %v-%v", rise, set, r2, s2)
	}
}

func TestMoonUpWindowLeavesOutMissingEndsInThePolarRegion(t *testing.T) {
	const lat, lon = 78.22, 15.65
	cases := []struct {
		name                       string
		at                         time.Time
		wantRise, wantSets, wantUp bool
	}{
		{"up for days", time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC), false, false, true},
		{"down for days", time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), false, false, false},
		{"risen, sets after the window", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), true, false, true},
	}
	for _, c := range cases {
		rise, set, up := moonUpWindow(lat, lon, c.at)
		if rise.IsZero() == c.wantRise || set.IsZero() == c.wantSets || up != c.wantUp {
			t.Errorf("%s: rise %v set %v up %v, want rise %v set %v up %v", c.name, rise, set, up, c.wantRise, c.wantSets, c.wantUp)
		}
	}
}

func TestMoonEventsFindTwoCrossingsInsideOneHour(t *testing.T) {
	near := func(got time.Time, h, m int, day int) bool {
		return got.Sub(time.Date(2026, 9, day, h, m, 0, 0, time.UTC)).Abs() <= 2*time.Minute
	}
	cases := []struct {
		name     string
		lat, lon float64
		day      int
		first    bool
		h1, m1   int
		h2, m2   int
	}{
		{"Longyearbyen, up 11:59-12:34", 78.22, 15.65, 13, true, 11, 59, 12, 34},
		{"Tromsø, down 12:17-12:56", 69.65, 18.96, 29, false, 12, 17, 12, 56},
	}
	for _, c := range cases {
		from := time.Date(2026, 9, c.day, 11, 0, 0, 0, time.UTC)
		evs := moonEvents(c.lat, c.lon, from, from.Add(2*time.Hour))
		if len(evs) != 2 || evs[0].rise != c.first || evs[1].rise == c.first ||
			!near(evs[0].at, c.h1, c.m1, c.day) || !near(evs[1].at, c.h2, c.m2, c.day) {
			t.Errorf("%s: events %+v, want rise=%v at %02d:%02d and the opposite at %02d:%02d", c.name, evs, c.first, c.h1, c.m1, c.h2, c.m2)
		}
	}
	rise, set, up := moonUpWindow(78.22, 15.65, time.Date(2026, 9, 13, 12, 15, 0, 0, time.UTC))
	if !up || !near(rise, 11, 59, 13) || !near(set, 12, 34, 13) {
		t.Errorf("Longyearbyen 12:15: up %v rise %v set %v, want up 11:59-12:34", up, rise, set)
	}
}

func TestTwilightCrossesItsAltitudes(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	obs := weatherObservation{TZKnown: true, TZOffsetSeconds: 3600}
	tw := localTwilight(lonLat, lonLon, now, obs)
	rise, set, _ := localSunTimes(lonLat, lonLon, now, obs)
	order := []time.Time{tw.Dawn, tw.BlueEnd, rise, tw.GoldenEnd, tw.GoldenStart, set, tw.BlueStart, tw.Dusk}
	for i := 1; i < len(order); i++ {
		if !order[i].After(order[i-1]) {
			t.Fatalf("crossing %d (%v) not after %d (%v)", i, order[i], i-1, order[i-1])
		}
	}
	for _, c := range []struct {
		at  time.Time
		alt float64
	}{{tw.Dawn, -6}, {tw.BlueEnd, -4}, {tw.GoldenEnd, 6}, {tw.GoldenStart, 6}, {tw.BlueStart, -4}, {tw.Dusk, -6}} {
		if got := solarElevation(lonLat, lonLon, c.at); math.Abs(got-c.alt) > 0.3 {
			t.Errorf("sun at %v is %.2f°, want %v°", c.at, got, c.alt)
		}
	}
}

type noaaSunReference struct {
	name     string
	lat, lon float64
	date     time.Time
	tz       int
	at       [8]int64
}

var noaaSun2026 = []noaaSunReference{
	{"London", 51.5072, -0.1276, time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), 3600,
		[8]int64{1782010519, 1782011689, 1782013385, 1782016641, 1782070039, 1782073295, 1782074991, 1782076161}},
	{"Sydney", -33.8688, 151.2093, time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), 36000,
		[8]int64{1781987534, 1781988171, 1781989197, 1781991496, 1782022531, 1782024830, 1782025856, 1782026494}},
	{"Quito", -0.1807, -78.4678, time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC), -18000,
		[8]int64{1774004239, 1774004719, 1774005479, 1774007118, 1774047430, 1774049070, 1774049830, 1774050309}},
	{"Berlin", 52.52, 13.405, time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC), 3600,
		[8]int64{1797834793, 1797835732, 1797837294, 1797841179, 1797860954, 1797864838, 1797866400, 1797867339}},
	{"Oslo", 59.9139, 10.7522, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 7200,
		[8]int64{1788233218, 1788234286, 1788235915, 1788239273, 1788283491, 1788286832, 1788288450, 1788289510}},
	{"Tromsø", 69.6492, 18.9553, time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), 7200,
		[8]int64{1787619950, 1787622163, 1787625074, 1787630321, 1787679104, 1787684291, 1787687140, 1787689278}},
}

func TestTwilightMatchesNOAA(t *testing.T) {
	names := [8]string{"dawn", "blue end", "sunrise", "golden end", "golden start", "sunset", "blue start", "dusk"}
	for _, ref := range noaaSun2026 {
		obs := weatherObservation{TZKnown: true, TZOffsetSeconds: ref.tz}
		now := ref.date.Add(12*time.Hour - time.Duration(ref.tz)*time.Second)
		tw := localTwilight(ref.lat, ref.lon, now, obs)
		rise, set, _ := localSunTimes(ref.lat, ref.lon, now, obs)
		got := [8]time.Time{tw.Dawn, tw.BlueEnd, rise, tw.GoldenEnd, tw.GoldenStart, set, tw.BlueStart, tw.Dusk}
		for i, g := range got {
			if d := g.Sub(time.Unix(ref.at[i], 0)); d.Abs() > 30*time.Second {
				t.Errorf("%s %s: %v, NOAA %v (%v off)", ref.name, names[i], g.UTC(), time.Unix(ref.at[i], 0).UTC(), d)
			}
		}
	}
}

func TestTwilightIsTheSolarDayOfTheLocalDate(t *testing.T) {
	const lat, lon = 64.1466, -21.9426
	obs := weatherObservation{TZKnown: true}
	cases := []struct {
		name       string
		now        time.Time
		dawn, dusk int64
	}{
		{"23:00, the dusk after midnight is today's", time.Date(2026, 7, 28, 23, 0, 0, 0, time.UTC), 1785206157, 1785284723},
		{"00:10, before that dusk, is the new day", time.Date(2026, 7, 29, 0, 10, 0, 0, time.UTC), 1785293033, 1785370685},
	}
	for _, c := range cases {
		tw := localTwilight(lat, lon, c.now, obs)
		if tw.Dawn.Sub(time.Unix(c.dawn, 0)).Abs() > 30*time.Second || tw.Dusk.Sub(time.Unix(c.dusk, 0)).Abs() > 30*time.Second {
			t.Errorf("Reykjavík %s: dawn %v dusk %v, want NOAA %v and %v", c.name, tw.Dawn, tw.Dusk, time.Unix(c.dawn, 0).UTC(), time.Unix(c.dusk, 0).UTC())
		}
	}
}

func TestTwilightLeavesOutCrossingsTheSunNeverMakes(t *testing.T) {
	tw := localTwilight(69.65, 18.96, time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), weatherObservation{})
	if !tw.Dawn.IsZero() || !tw.Dusk.IsZero() || !tw.BlueEnd.IsZero() || !tw.BlueStart.IsZero() {
		t.Errorf("midnight sun has twilight: %+v", tw)
	}
	if tw.GoldenEnd.IsZero() || tw.GoldenStart.IsZero() {
		t.Errorf("midnight sun dips below 6°, golden crossings missing: %+v", tw)
	}
}

func TestSunCrossingsSitAtTheirAltitudeAllYear(t *testing.T) {
	sites := []struct {
		name     string
		lat, lon float64
	}{{"Longyearbyen", 78.22, 15.65}, {"McMurdo", -77.85, 166.67}}
	for _, s := range sites {
		for _, alt := range []float64{sunHorizonDeg, civilTwilightDeg, blueHourEndDeg, goldenHourEndDeg} {
			for d := range 365 {
				day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, d)
				rise, set, _ := sunAt(s.lat, s.lon, day, alt)
				for _, e := range []time.Time{rise, set} {
					if e.IsZero() {
						continue
					}
					if got := solarElevation(s.lat, s.lon, e); math.Abs(got-alt) > 0.08 {
						t.Errorf("%s %s: crossing of %.3f° at %v has the sun at %.3f°", s.name, day.Format("2006-01-02"), alt, e, got)
					}
				}
			}
		}
	}
}

func TestSunsetBeforePolarDayIsLeftOut(t *testing.T) {
	rise, set, ok := sunTimes(78.22, 15.65, time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC))
	if ok || rise.IsZero() || !set.IsZero() {
		t.Errorf("Longyearbyen 2026-04-18: rise %v set %v ok %v, want the sunrise and no sunset", rise, set, ok)
	}
}

func TestSunCrossingsConvergeOnTheElevationCrossing(t *testing.T) {
	sites := []struct {
		name     string
		lat, lon float64
	}{{"Longyearbyen", 78.22, 15.65}, {"McMurdo", -77.85, 166.67}, {"Tromsø", 69.65, 18.96}}
	const step = 5 * time.Second
	for _, s := range sites {
		for _, alt := range []float64{sunHorizonDeg, civilTwilightDeg, blueHourEndDeg, goldenHourEndDeg} {
			above := func(at time.Time) bool { return solarElevation(s.lat, s.lon, at) > alt }
			for d := range 365 {
				day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, d)
				rise, set, _ := sunAt(s.lat, s.lon, day, alt)
				for _, e := range []time.Time{rise, set} {
					if e.IsZero() {
						continue
					}
					best := time.Duration(-1)
					for at := e.Add(-10 * time.Minute); at.Before(e.Add(10 * time.Minute)); at = at.Add(step) {
						if above(at) != above(at.Add(step)) {
							if off := at.Add(step / 2).Sub(e).Abs(); best < 0 || off < best {
								best = off
							}
						}
					}
					if best < 0 || best > 10*time.Second {
						t.Errorf("%s %s: crossing of %.3f° at %v is %v from where the sun crosses it (-1 = none within 10 min)", s.name, day.Format("2006-01-02"), alt, e, best)
					}
				}
			}
		}
	}
}
