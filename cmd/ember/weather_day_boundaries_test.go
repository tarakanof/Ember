package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type dstCase struct {
	name   string
	dates  []string
	offset int
	now    time.Time
}

var belgradeDST = []dstCase{
	{"spring forward 2027-03-28", []string{"2027-03-27", "2027-03-28", "2027-03-29", "2027-03-30"}, 3600, time.Date(2027, 3, 27, 10, 0, 0, 0, time.UTC)},
	{"fall back 2026-10-25", []string{"2026-10-24", "2026-10-25", "2026-10-26", "2026-10-27"}, 7200, time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)},
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
	return d
}

func assertDayLabels(t *testing.T, days []weatherDay, offset int, want []string) {
	t.Helper()
	if len(days) < len(want) {
		t.Fatalf("%d days, want at least %d", len(days), len(want))
	}
	for i, w := range want {
		label := days[i].Start.Add(time.Duration(offset) * time.Second).UTC().Format("2006-01-02 15:04")
		if label != w+" 00:00" {
			t.Errorf("day %d labels as %s under the single offset %d, want %s 00:00", i, label, offset, w)
		}
	}
}

func stubJSON(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestOpenMeteoDaysUseTheSingleOffsetAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			var times []string
			for _, d := range c.dates {
				times = append(times, fmt.Sprint(belgradeMidnight(t, d).Unix()))
			}
			body := fmt.Sprintf(`{"utc_offset_seconds":%d,"current":{"time":%d,"temperature_2m":5,"weather_code":0},
"hourly":{"time":[%d],"temperature_2m":[5]},"daily":{"time":[%s],"weather_code":[0,1,2,3]}}`,
				c.offset, c.now.Unix(), c.now.Truncate(time.Hour).Unix(), strings.Join(times, ","))
			wf := newWeatherFetcher()
			wf.openMeteoBase = stubJSON(t, body)
			obs, err := wf.fetchOpenMeteo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5})
			if err != nil {
				t.Fatal(err)
			}
			assertDayLabels(t, obs.Days, c.offset, c.dates)
			if obs.Days[3].Code != "3" {
				t.Errorf("day 3 code = %q, want the fourth day's own values kept", obs.Days[3].Code)
			}
		})
	}
}

func TestMetNoDaysUseTheSingleOffsetAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			start := belgradeMidnight(t, c.dates[0]).Add(time.Hour)
			var entries []string
			for i := range 72 {
				entries = append(entries, fmt.Sprintf(`{"time":%q,"data":{"instant":{"details":{"air_temperature":5}},"next_1_hours":{"summary":{"symbol_code":"cloudy"},"details":{"precipitation_amount":0}}}}`,
					start.Add(time.Duration(i)*time.Hour).UTC().Format(time.RFC3339)))
			}
			wf := newWeatherFetcher()
			wf.metNoBase = stubJSON(t, `{"properties":{"timeseries":[`+strings.Join(entries, ",")+`]}}`)
			res, err := wf.fetchMetNo(t.Context(), WeatherConfig{Latitude: 44.8, Longitude: 20.5}, fetchHint{TZOffsetSeconds: c.offset, TZKnown: true})
			if err != nil {
				t.Fatal(err)
			}
			assertDayLabels(t, res.Obs.Days, c.offset, c.dates[:3])
		})
	}
}

func TestTomorrowDaysUseTheSingleOffsetAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			var daily []string
			for _, d := range c.dates {
				daily = append(daily, fmt.Sprintf(`{"time":%q,"values":{"temperatureMax":9}}`, belgradeMidnight(t, d).Add(6*time.Hour).UTC().Format(time.RFC3339)))
			}
			body := fmt.Sprintf(`{"timelines":{"hourly":[{"time":%q,"values":{"temperature":5,"weatherCode":1000}}],"daily":[%s]}}`,
				c.now.UTC().Format(time.RFC3339), strings.Join(daily, ","))
			st := newTomorrowStub(t, []byte(body))
			obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: c.offset, TZKnown: true})
			if err != nil {
				t.Fatal(err)
			}
			assertDayLabels(t, obs.Days, c.offset, c.dates)
		})
	}
}

func TestKnobDaysCarryTheirEndAcrossDST(t *testing.T) {
	for _, c := range belgradeDST {
		t.Run(c.name, func(t *testing.T) {
			var days []weatherDay
			for _, d := range c.dates {
				date, _ := time.Parse("2006-01-02", d)
				days = append(days, weatherDay{Start: date.Add(-time.Duration(c.offset) * time.Second), Cond: "clear", Code: "0"})
			}
			obs := weatherObservation{Condition: "clear", ConditionCode: "0", TempC: 5, TZOffsetSeconds: c.offset, TZKnown: true, Days: days, FetchedAt: c.now}
			cfg := WeatherConfig{Enabled: true, Latitude: 44.8, Longitude: 20.5}
			d := newKnobWeatherDetail(newWeatherState(cfg, obs, true, airObservation{}, false, c.now), cfg)
			if d.UTCOffset != int64(c.offset) || len(d.Days) != 3 {
				t.Fatalf("utc_offset %d, %d days", d.UTCOffset, len(d.Days))
			}
			for i, kd := range d.Days {
				if kd.End != kd.T+86400 {
					t.Errorf("day %d end %d, want t + 24 h (%d)", i, kd.End, kd.T+86400)
				}
				if i+1 < len(d.Days) && kd.End != d.Days[i+1].T {
					t.Errorf("day %d end %d, want the next day's t %d", i, kd.End, d.Days[i+1].T)
				}
				label := time.Unix(kd.End+d.UTCOffset, 0).UTC().Format("15:04")
				if label != "00:00" {
					t.Errorf("day %d end labels as %s under utc_offset, want 00:00", i, label)
				}
			}
		})
	}
}
