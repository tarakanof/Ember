package main

import (
	"archive/zip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type dstCase struct {
	name   string
	dates  []string
	change string
	length time.Duration
	offset int
	now    time.Time
	late   time.Time
}

var belgradeDST = []dstCase{
	{"spring forward 2027-03-28", []string{"2027-03-27", "2027-03-28", "2027-03-29", "2027-03-30"}, "2027-03-28", 23 * time.Hour, 3600,
		time.Date(2027, 3, 27, 10, 0, 0, 0, time.UTC), time.Date(2027, 3, 28, 21, 30, 0, 0, time.UTC)},
	{"fall back 2026-10-25", []string{"2026-10-24", "2026-10-25", "2026-10-26", "2026-10-27"}, "2026-10-25", 25 * time.Hour, 7200,
		time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 22, 30, 0, 0, time.UTC)},
}

func belgradeMidnight(t *testing.T, date string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatal(err)
	}
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatal(err)
	}
	return d.UTC()
}

func nextDate(t *testing.T, date string) string {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	return d.AddDate(0, 0, 1).Format("2006-01-02")
}

func assertTrueMidnights(t *testing.T, c dstCase, days []weatherDay, n int) {
	t.Helper()
	if len(days) < n {
		t.Fatalf("%d days, want at least %d", len(days), n)
	}
	for i, date := range c.dates[:n] {
		start, end := belgradeMidnight(t, date), belgradeMidnight(t, nextDate(t, date))
		if !days[i].Start.Equal(start) || !dayEnd(days[i]).Equal(end) {
			t.Errorf("day %d (%s) = [%v, %v), want Belgrade's true midnights [%v, %v)", i, date, days[i].Start, dayEnd(days[i]), start, end)
		}
		if date == c.change && dayEnd(days[i]).Sub(days[i].Start) != c.length {
			t.Errorf("DST day %s lasts %v, want %v", date, dayEnd(days[i]).Sub(days[i].Start), c.length)
		}
	}
}

func stubJSON(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func openMeteoDSTBody(t *testing.T, c dstCase, zone string) string {
	t.Helper()
	var times []string
	for _, d := range c.dates {
		date, _ := time.Parse("2006-01-02", d)
		times = append(times, fmt.Sprint(date.Add(-time.Duration(c.offset)*time.Second).Unix()))
	}
	tz := ""
	if zone != "" {
		tz = fmt.Sprintf(`"timezone":%q,`, zone)
	}
	return fmt.Sprintf(`{%s"utc_offset_seconds":%d,"current":{"time":%d,"temperature_2m":5,"weather_code":0},
"hourly":{"time":[%d],"temperature_2m":[5]},"daily":{"time":[%s],"weather_code":[0,1,2,3]}}`,
		tz, c.offset, c.now.Unix(), c.now.Truncate(time.Hour).Unix(), strings.Join(times, ","))
}

func TestOpenMeteoDaysAreTrueLocalMidnightsAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			wf := newWeatherFetcher()
			wf.openMeteoBase = stubJSON(t, openMeteoDSTBody(t, c, "Europe/Belgrade"))
			obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5})
			if err != nil {
				t.Fatal(err)
			}
			assertTrueMidnights(t, c, obs.Days, 4)
			for i, want := range []string{"0", "1", "2", "3"} {
				if obs.Days[i].Code != want {
					t.Errorf("day %d code %q, want %q: aggregates stay on their date", i, obs.Days[i].Code, want)
				}
			}
		})
	}
}

func TestOpenMeteoDaysWithoutAZoneEndADayLater(t *testing.T) {
	c := belgradeDST[0]
	wf := newWeatherFetcher()
	wf.openMeteoBase = stubJSON(t, openMeteoDSTBody(t, c, ""))
	obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5})
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range obs.Days {
		date, _ := time.Parse("2006-01-02", c.dates[i])
		if start := date.Add(-time.Duration(c.offset) * time.Second); !d.Start.Equal(start) || !dayEnd(d).Equal(start.Add(24*time.Hour)) {
			t.Errorf("day %d = [%v, %v), want midnight under the single offset and + 24 h without a zone", i, d.Start, dayEnd(d))
		}
	}
}

func TestMetNoDaysAreTrueLocalMidnightsAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			start := belgradeMidnight(t, c.dates[0]).Add(time.Hour)
			var entries []string
			for i := range 72 {
				entries = append(entries, fmt.Sprintf(`{"time":%q,"data":{"instant":{"details":{"air_temperature":%d}},"next_1_hours":{"summary":{"symbol_code":"cloudy"},"details":{"precipitation_amount":1}}}}`,
					start.Add(time.Duration(i)*time.Hour).UTC().Format(time.RFC3339), i))
			}
			wf := newWeatherFetcher()
			wf.metNoBase = stubJSON(t, `{"properties":{"timeseries":[`+strings.Join(entries, ",")+`]}}`)
			res, err := wf.fetchMetNo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5},
				fetchHint{TZOffsetSeconds: c.offset, TZKnown: true, TZName: "Europe/Belgrade"})
			if err != nil {
				t.Fatal(err)
			}
			assertTrueMidnights(t, c, res.Obs.Days, 3)
			for i, d := range res.Obs.Days[:3] {
				hours := dayEnd(d).Sub(d.Start).Hours()
				if i == 0 {
					hours--
				}
				if !approx(d.PrecipMM, hours) {
					t.Errorf("day %d precipitation %v mm, want %v (1 mm for each of its own hours)", i, valueOr(d.PrecipMM, -1), hours)
				}
			}
		})
	}
}

func TestTomorrowDaysAreTrueLocalMidnightsAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			var daily []string
			for _, d := range c.dates {
				daily = append(daily, fmt.Sprintf(`{"time":%q,"values":{"temperatureMax":9}}`, belgradeMidnight(t, d).Add(6*time.Hour).UTC().Format(time.RFC3339)))
			}
			body := fmt.Sprintf(`{"timelines":{"hourly":[{"time":%q,"values":{"temperature":5,"weatherCode":1000}}],"daily":[%s]}}`,
				c.now.UTC().Format(time.RFC3339), strings.Join(daily, ","))
			st := newTomorrowStub(t, []byte(body))
			obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade,
				fetchHint{TZOffsetSeconds: c.offset, TZKnown: true, TZName: "Europe/Belgrade"})
			if err != nil {
				t.Fatal(err)
			}
			assertTrueMidnights(t, c, obs.Days, 4)
		})
	}
}

func TestWeatherAPIDaysAreTrueLocalMidnightsAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			st := newWAStub(t, waDSTBody(t, c.dates[:3], c.now))
			obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, fetchHint{})
			if err != nil {
				t.Fatal(err)
			}
			assertTrueMidnights(t, c, obs.Days, 3)
		})
	}
}

func TestStaleKnobViewKeepsTrueDayBoundariesAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			wf := newWeatherFetcher()
			wf.openMeteoBase = stubJSON(t, openMeteoDSTBody(t, c, "Europe/Belgrade"))
			obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5})
			if err != nil {
				t.Fatal(err)
			}
			obs.FetchedAt = c.now
			cfg := WeatherConfig{Enabled: true, Latitude: 44.8, Longitude: 20.5}
			view := func(at time.Time) *knobWeatherDetail {
				return newKnobWeatherDetail(newWeatherState(cfg, obs, true, airObservation{}, false, at), cfg)
			}

			d := view(c.now.Add(2 * time.Hour))
			if len(d.Days) != 3 || d.Days[0].T != belgradeMidnight(t, c.change).Unix() {
				t.Fatalf("days = %+v, want the DST day %s first", d.Days, c.change)
			}
			if want := belgradeMidnight(t, nextDate(t, c.change)).Unix(); d.Days[0].End != want || d.Days[0].End == d.Days[0].T+86400 {
				t.Errorf("DST day end = %d, want the true next midnight %d (%v after t, not 24 h)", d.Days[0].End, want, c.length)
			}
			for i, kd := range d.Days {
				if want := belgradeMidnight(t, nextDate(t, c.dates[i+1])).Unix(); kd.End != want {
					t.Errorf("day %d end = %d, want %d", i, kd.End, want)
				}
			}

			late := view(c.late)
			if len(late.Days) == 0 || late.Days[0].T != belgradeMidnight(t, nextDate(t, c.change)).Unix() {
				t.Errorf("at %v (23:30 local on %s) the next day listed is %+v, want %s: today is still %s",
					c.late, c.change, late.Days, nextDate(t, c.change), c.change)
			}
		})
	}
}

func TestAirReadingPassesItsZoneToTheHint(t *testing.T) {
	wf := newWeatherFetcher()
	wf.airQualityBase = stubJSON(t, `{"timezone":"Europe/Belgrade","utc_offset_seconds":7200,"current":{"european_aqi":20}}`)
	air, err := wf.fetchAirQuality(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5})
	if err != nil {
		t.Fatal(err)
	}
	if air.TZName != "Europe/Belgrade" {
		t.Fatalf("air zone = %q", air.TZName)
	}
	now := time.Now()
	air.FetchedAt = now
	s := newWeatherStore()
	s.air, s.haveAir = air, true
	cfg := WeatherConfig{AirTile: boolPtr(true)}
	if h, _ := s.fetchHint(cfg, now); h.TZName != "Europe/Belgrade" || h.TZOffsetSeconds != 7200 {
		t.Errorf("hint = %+v, want the air reading's zone and offset", h)
	}
}

func firstInstant(t *testing.T, loc *time.Location, y int, m time.Month, d int) time.Time {
	t.Helper()
	want := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	for at := want.Add(-48 * time.Hour); at.Before(want.Add(48 * time.Hour)); at = at.Add(time.Minute) {
		ly, lm, ld := at.In(loc).Date()
		if ly == want.Year() && lm == want.Month() && ld == want.Day() {
			return at.UTC()
		}
	}
	t.Fatalf("no instant of %v in %s", want.Format("2006-01-02"), loc)
	return time.Time{}
}

var midnightGaps = []struct {
	zone string
	date time.Time
}{
	{"America/Santiago", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)},
	{"America/Havana", time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)},
}

func TestDayClockStartsAMidnightlessDayAtItsFirstInstant(t *testing.T) {
	for _, g := range midnightGaps {
		t.Run(g.zone, func(t *testing.T) {
			loc := loadZone(g.zone)
			if loc == nil {
				t.Fatal("zone not loaded")
			}
			c := dayClock{loc: loc}
			y, m, d := g.date.Date()
			start := c.midnight(y, m, d)
			want := firstInstant(t, loc, y, m, d)
			if !start.Equal(want) || start.In(loc).Hour() != 1 {
				t.Errorf("start of %s = %v, want its first instant %v (01:00 local)", g.date.Format("2006-01-02"), start.In(loc), want.In(loc))
			}
			prev := c.midnight(y, m, d-1)
			if end := c.after(prev); !end.Equal(start) {
				t.Errorf("day before ends %v, want %v", end.In(loc), start.In(loc))
			}
			if end := c.after(start); !end.Equal(firstInstant(t, loc, y, m, d+1)) || end.Sub(start) != 23*time.Hour {
				t.Errorf("gap day = [%v, %v), want 23 h to the next midnight", start.In(loc), end.In(loc))
			}
			if s := c.startOf(start.Add(30 * time.Minute)); !s.Equal(start) {
				t.Errorf("startOf(01:30) = %v, want %v", s.In(loc), start.In(loc))
			}
		})
	}
}

func metHourly(from time.Time, n int) []metEntry {
	ts := make([]metEntry, n)
	for i := range ts {
		ts[i].Time = from.Add(time.Duration(i) * time.Hour)
		ts[i].Data.Next1Hours = &metPeriod{}
		ts[i].Data.Next1Hours.Details.PrecipMM = new(1.0)
		ts[i].Data.Next1Hours.Summary.SymbolCode = "rain"
	}
	return ts
}

func metDaysWithin(t *testing.T, ts []metEntry, c dayClock, where string) []weatherDay {
	t.Helper()
	done := make(chan []weatherDay, 1)
	go func() { done <- metDays(ts, c) }()
	select {
	case days := <-done:
		return days
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: metDays did not return", where)
		return nil
	}
}

func TestProvidersSplitAMidnightlessDayAtItsFirstInstant(t *testing.T) {
	for _, g := range midnightGaps {
		t.Run(g.zone, func(t *testing.T) {
			loc := loadZone(g.zone)
			y, m, d := g.date.Date()
			want := []time.Time{firstInstant(t, loc, y, m, d-1), firstInstant(t, loc, y, m, d), firstInstant(t, loc, y, m, d+1), firstInstant(t, loc, y, m, d+2)}
			check := func(name string, days []weatherDay, n int) {
				t.Helper()
				if len(days) < n {
					t.Fatalf("%s: %d days, want %d", name, len(days), n)
				}
				for i := range n {
					if !days[i].Start.Equal(want[i]) || !dayEnd(days[i]).Equal(want[i+1]) {
						t.Errorf("%s day %d = [%v, %v), want [%v, %v)", name, i, days[i].Start.In(loc), dayEnd(days[i]).In(loc), want[i].In(loc), want[i+1].In(loc))
					}
				}
			}
			_, off := want[0].In(loc).Zone()
			clock := dayClock{loc: loc, off: time.Duration(off) * time.Second}

			days := metDaysWithin(t, metHourly(want[0], 72), clock, "MET")
			check("MET", days, 2)
			if len(days) > 1 && !approx(days[1].PrecipMM, 23) {
				t.Errorf("MET gap day precipitation %v, want 23 (its 23 hours)", valueOr(days[1].PrecipMM, -1))
			}

			var waDaysJSON []string
			for i := range 3 {
				waDaysJSON = append(waDaysJSON, fmt.Sprintf(`{"date":%q,"day":{"maxtemp_c":10},"hour":[]}`, g.date.AddDate(0, 0, i-1).Format("2006-01-02")))
			}
			st := newWAStub(t, []byte(fmt.Sprintf(`{"location":{"tz_id":%q},"current":{"last_updated_epoch":%d,"temp_c":5},"forecast":{"forecastday":[%s]}}`,
				g.zone, want[0].Add(10*time.Hour).Unix(), strings.Join(waDaysJSON, ","))))
			obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, fetchHint{})
			if err != nil {
				t.Fatal(err)
			}
			check("WeatherAPI.com", obs.Days, 3)

			var daily []string
			for i := range 3 {
				daily = append(daily, fmt.Sprintf(`{"time":%q,"values":{"temperatureMax":9}}`, want[i].In(loc).Add(6*time.Hour).UTC().Format(time.RFC3339)))
			}
			ts := newTomorrowStub(t, []byte(fmt.Sprintf(`{"timelines":{"hourly":[{"time":%q,"values":{"temperature":5,"weatherCode":1000}}],"daily":[%s]}}`,
				want[0].Add(10*time.Hour).Format(time.RFC3339), strings.Join(daily, ","))))
			tobs, err := newTomorrowFetcher(ts.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: off, TZKnown: true, TZName: g.zone})
			if err != nil {
				t.Fatal(err)
			}
			check("Tomorrow.io", tobs.Days, 3)

			var times []string
			for i := range 4 {
				times = append(times, fmt.Sprint(g.date.AddDate(0, 0, i-1).Add(-time.Duration(off)*time.Second).Unix()))
			}
			wf := newWeatherFetcher()
			wf.openMeteoBase = stubJSON(t, fmt.Sprintf(`{"timezone":%q,"utc_offset_seconds":%d,"current":{"time":%d,"temperature_2m":5,"weather_code":0},
"hourly":{"time":[%d],"temperature_2m":[5]},"daily":{"time":[%s],"weather_code":[0,1,2,3]}}`,
				g.zone, off, want[0].Add(10*time.Hour).Unix(), want[0].Add(10*time.Hour).Unix(), strings.Join(times, ",")))
			oobs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: -33.4, Longitude: -70.6})
			if err != nil {
				t.Fatal(err)
			}
			check("Open-Meteo", oobs.Days, 3)
		})
	}
}

func TestDayClockOverEveryZoneForTwoYears(t *testing.T) {
	zr, err := zip.OpenReader(filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip"))
	if err != nil {
		t.Skipf("no zone list: %v", err)
	}
	defer zr.Close()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	zones := 0
	for _, f := range zr.File {
		loc := loadZone(f.Name)
		if loc == nil {
			continue
		}
		zones++
		c := dayClock{loc: loc}
		y, m, d := from.Date()
		prevEnd := c.midnight(y, m, d)
		var transitions []time.Time
		for i := range 731 {
			start := c.midnight(y, m, d+i)
			end := c.after(start)
			if !end.After(start) {
				t.Fatalf("%s %s: [%v, %v) is empty", f.Name, from.AddDate(0, 0, i).Format("2006-01-02"), start, end)
			}
			if !start.Equal(prevEnd) {
				t.Fatalf("%s %s: starts %v, the day before ended %v", f.Name, from.AddDate(0, 0, i).Format("2006-01-02"), start.In(loc), prevEnd.In(loc))
			}
			if !c.startOf(start).Equal(start) || !c.startOf(end.Add(-time.Second)).Equal(start) {
				t.Fatalf("%s %s: startOf disagrees with [%v, %v)", f.Name, from.AddDate(0, 0, i).Format("2006-01-02"), start.In(loc), end.In(loc))
			}
			if l := end.Sub(start); l != 24*time.Hour {
				transitions = append(transitions, start)
			}
			prevEnd = end
		}
		for _, tr := range transitions {
			_, off := tr.In(loc).Zone()
			metDaysWithin(t, metHourly(c.midnight(tr.In(loc).AddDate(0, 0, -1).Date()), 72), dayClock{loc: loc, off: time.Duration(off) * time.Second}, f.Name+" "+tr.String())
		}
	}
	if zones < 300 {
		t.Fatalf("checked only %d zones", zones)
	}
}
