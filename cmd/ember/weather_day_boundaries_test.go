package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/weather"
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

func TestStaleKnobViewKeepsTrueDayBoundariesAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			wf := weather.NewFetcher()
			wf.OpenMeteoBase = stubJSON(t, openMeteoDSTBody(t, c, "Europe/Belgrade"))
			obs, err := wf.FetchOpenMeteo(t.Context(), weather.Config{Latitude: 44.8, Longitude: 20.5})
			if err != nil {
				t.Fatal(err)
			}
			obs.FetchedAt = c.now
			cfg := weather.Config{Enabled: true, Latitude: 44.8, Longitude: 20.5}
			view := func(at time.Time) *knobWeatherDetail {
				return newKnobWeatherDetail(newWeatherState(cfg, obs, true, weather.AirObservation{}, false, at), cfg)
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
	wf := weather.NewFetcher()
	wf.AirQualityBase = stubJSON(t, `{"timezone":"Europe/Belgrade","utc_offset_seconds":7200,"current":{"european_aqi":20}}`)
	air, err := wf.FetchAirQuality(t.Context(), weather.Config{Latitude: 44.8, Longitude: 20.5})
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
	cfg := weather.Config{AirTile: boolPtr(true)}
	if h, _ := s.fetchHint(cfg, now); h.TZName != "Europe/Belgrade" || h.TZOffsetSeconds != 7200 {
		t.Errorf("hint = %+v, want the air reading's zone and offset", h)
	}
}
