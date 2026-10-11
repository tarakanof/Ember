package main

import (
	"testing"
	"time"
)

func precipSeries(start time.Time, step time.Duration, mmh []float64, snow ...int) []precipStep {
	out := make([]precipStep, len(mmh))
	for i, v := range mmh {
		out[i] = precipStep{At: start.Add(time.Duration(i) * step), MMh: v}
	}
	for _, i := range snow {
		out[i].Snow = true
	}
	return out
}

func TestRainSpells(t *testing.T) {
	t0 := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	q := 15 * time.Minute
	at := func(steps int) time.Time { return t0.Add(time.Duration(steps) * q) }
	cases := []struct {
		name      string
		steps     []precipStep
		step      time.Duration
		now       time.Time
		wantNow   int
		haveNow   bool
		wantUntil time.Time
		want      []rainSpell
	}{
		{name: "dry", steps: precipSeries(t0, q, []float64{0, 0.05, 0}), step: q, now: t0, haveNow: true, want: []rainSpell{}},
		{name: "a 30-min gap merges, peak intensity", step: q, now: at(1), haveNow: true, wantNow: 1, wantUntil: at(6),
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0, 8, 3}),
			want:  []rainSpell{{Start: at(0), End: at(6), Intensity: 3}}},
		{name: "a 45-min gap splits", step: q, now: at(2), haveNow: true,
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0, 0, 3}),
			want:  []rainSpell{{Start: at(5), End: at(6), Intensity: 2}}},
		{name: "snow when most wet steps are snow", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(3 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{0.5, 0.5, 0.5}, 1, 2),
			want:  []rainSpell{{Start: t0, End: t0.Add(3 * time.Hour), Intensity: 1, Snow: true}}},
		{name: "half snow is rain", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(2 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{0.5, 0.5}, 1),
			want:  []rainSpell{{Start: t0, End: t0.Add(2 * time.Hour), Intensity: 1}}},
		{name: "beyond 12 h and after four are left out", step: 3 * time.Hour, now: t0, haveNow: true,
			steps: precipSeries(t0, 3*time.Hour, []float64{0, 1, 0, 1, 0, 1, 0, 1, 0, 1}),
			want: []rainSpell{{Start: t0.Add(3 * time.Hour), End: t0.Add(6 * time.Hour), Intensity: 1},
				{Start: t0.Add(9 * time.Hour), End: t0.Add(12 * time.Hour), Intensity: 1}}},
		{name: "no step covers now", step: time.Hour, now: t0.Add(-2 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{1}),
			want:  []rainSpell{{Start: t0, End: t0.Add(time.Hour), Intensity: 1}}},
		{name: "an ended spell is dropped", step: q, now: at(3), haveNow: true,
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0}), want: []rainSpell{}},
		{name: "max four", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{1, 0, 1, 0, 1, 0, 1, 0, 1}),
			want: []rainSpell{{Start: t0, End: t0.Add(time.Hour), Intensity: 1}, {Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Intensity: 1},
				{Start: t0.Add(4 * time.Hour), End: t0.Add(5 * time.Hour), Intensity: 1}, {Start: t0.Add(6 * time.Hour), End: t0.Add(7 * time.Hour), Intensity: 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rainNow, haveNow, until, spells := rainSpells(c.steps, c.step, c.now)
			if rainNow != c.wantNow || haveNow != c.haveNow || !until.Equal(c.wantUntil) {
				t.Errorf("now = %d (have %v), until %v; want %d (have %v), until %v", rainNow, haveNow, until, c.wantNow, c.haveNow, c.wantUntil)
			}
			if spells == nil || len(spells) != len(c.want) {
				t.Fatalf("spells = %#v, want %+v", spells, c.want)
			}
			for i := range spells {
				if spells[i] != c.want[i] {
					t.Errorf("spell %d = %+v, want %+v", i, spells[i], c.want[i])
				}
			}
		})
	}
}

func TestRainIntensityBands(t *testing.T) {
	for _, c := range []struct {
		mmh  float64
		want int
	}{{0, 0}, {0.099, 0}, {0.1, 1}, {2.49, 1}, {2.5, 2}, {7.59, 2}, {7.6, 3}, {50, 3}} {
		if got := rainIntensity(c.mmh); got != c.want {
			t.Errorf("rainIntensity(%v) = %d, want %d", c.mmh, got, c.want)
		}
	}
}

func TestFaceDaysWidenTodayToTheCurrentTemperature(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	day := func(d int, lo, hi float64) weatherDay {
		return weatherDay{Start: time.Date(2026, 6, 21+d, 0, 0, 0, 0, time.UTC), MinC: &lo, MaxC: &hi}
	}
	obs := weatherObservation{TempC: 21, Days: []weatherDay{day(-1, 0, 1), day(0, 9, 14), day(1, 1, 2), day(2, 1, 2), day(3, 1, 2), day(4, 1, 2)}}
	today, next := faceDays(obs, now)
	if today == nil || *today.MinC != 9 || *today.MaxC != 21 {
		t.Fatalf("today = %+v, want 9..21", today)
	}
	if *obs.Days[1].MaxC != 14 {
		t.Error("widening wrote through to the stored observation")
	}
	if len(next) != 3 || !next[0].Start.Equal(obs.Days[2].Start) {
		t.Errorf("next days = %+v, want the 3 after today", next)
	}
	obs.TempC = -3
	if today, _ = faceDays(obs, now); *today.MinC != -3 || *today.MaxC != 14 {
		t.Errorf("cold today = %v..%v, want -3..14", *today.MinC, *today.MaxC)
	}
	if today, next = faceDays(weatherObservation{TempC: 5}, now); today != nil || next != nil {
		t.Errorf("no days: today %+v next %+v, want none", today, next)
	}
}
