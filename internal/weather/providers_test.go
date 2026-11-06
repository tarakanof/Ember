package weather

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestFetchOpenMeteoModelQuery(t *testing.T) {
	cases := []struct {
		model   string
		want    string
		present bool
	}{
		{"", "", false},
		{"best_match", "", false},
		{"ecmwf_ifs", "ecmwf_ifs", true},
		{"ukmo_seamless", "ukmo_seamless", true},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			var got []string
			var has bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, has = r.URL.Query()["models"], r.URL.Query().Has("models")
				_, _ = w.Write([]byte(openMeteoFacesBody))
			}))
			defer srv.Close()
			wf := NewFetcher()
			wf.OpenMeteoBase = srv.URL
			cfg := Config{Provider: "open-meteo", OpenMeteoModel: c.model, Latitude: 44.8, Longitude: 20.5}
			if _, err := wf.Fetch(t.Context(), cfg, FetchHint{}); err != nil {
				t.Fatal(err)
			}
			if has != c.present || (c.present && (len(got) != 1 || got[0] != c.want)) {
				t.Errorf("models = %v (present %v), want %q (present %v)", got, has, c.want, c.present)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"120", 2 * time.Minute},
		{"-5", 0},
		{"-99999999999999999999", 0},
		{"soon", 0},
		{"1.5", 0},
		{"31536000", RetryAfterMax},
		{"9300000000", RetryAfterMax},
		{"99999999999999999999", RetryAfterMax},
		{"Thu, 01 Jan 2099 00:00:00 GMT", RetryAfterMax},
		{now.Add(10 * time.Minute).Format(http.TimeFormat), 10 * time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in, now); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

const openMeteoFacesBody = `{"utc_offset_seconds":7200,
"current":{"time":1782036000,"temperature_2m":12.5,"weather_code":61,"cloud_cover":94,"uv_index":2.6,"wind_speed_10m":21.2,"wind_gusts_10m":38.4,"wind_direction_10m":225},
"minutely_15":{"time":[1782036000,1782036900,1782037800,1782038700],"precipitation":[0.0,0.3,null,0.5],"snowfall":[0,0,0,0.2]},
"hourly":{"time":[1782036000],"temperature_2m":[12.5]},
"daily":{"time":[1781992800,1782079200],"weather_code":[61,3],"temperature_2m_max":[14.2,16],"temperature_2m_min":[9.1,7],
"precipitation_sum":[2.1,0],"precipitation_probability_max":[83,null],"uv_index_max":[4.1,null],"wind_gusts_10m_max":[41,30]}}`

const metCompleteBody = `{"properties":{"timeseries":[
{"time":"2026-06-21T20:00:00Z","data":{"instant":{"details":{"air_temperature":14,"cloud_area_fraction":80,"ultraviolet_index_clear_sky":0.5,"wind_speed":5,"wind_speed_of_gust":10,"wind_from_direction":225}},
 "next_1_hours":{"summary":{"symbol_code":"lightrain"},"details":{"precipitation_amount":0.5}},
 "next_6_hours":{"summary":{"symbol_code":"rain"},"details":{"air_temperature_min":9,"air_temperature_max":14,"precipitation_amount":2,"probability_of_precipitation":60}}}},
{"time":"2026-06-21T21:00:00Z","data":{"instant":{"details":{"air_temperature":13,"wind_speed_of_gust":12}},
 "next_1_hours":{"summary":{"symbol_code":"lightsleet"},"details":{"precipitation_amount":1.0}}}},
{"time":"2026-06-21T22:00:00Z","data":{"instant":{"details":{"air_temperature":12}},
 "next_1_hours":{"summary":{"symbol_code":"cloudy"},"details":{"precipitation_amount":0}},
 "next_6_hours":{"summary":{"symbol_code":"cloudy"},"details":{"air_temperature_min":8,"air_temperature_max":12,"precipitation_amount":0.6,"probability_of_precipitation":20}}}},
{"time":"2026-06-22T00:00:00Z","data":{"instant":{"details":{"air_temperature":11}},
 "next_6_hours":{"summary":{"symbol_code":"heavyrainandthunder"},"details":{"air_temperature_min":7,"air_temperature_max":15,"precipitation_amount":3,"probability_of_precipitation":70}}}},
{"time":"2026-06-22T06:00:00Z","data":{"instant":{"details":{"air_temperature":15}},
 "next_6_hours":{"summary":{"symbol_code":"rain"},"details":{"air_temperature_min":10,"air_temperature_max":18,"precipitation_amount":1,"probability_of_precipitation":40}}}},
{"time":"2026-06-22T12:00:00Z","data":{"instant":{"details":{"air_temperature":18}}}}
]}}`

func approx(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }

func TestFetchOpenMeteoRequestsAndParsesFaceData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		want := map[string]string{
			"current":              "temperature_2m,weather_code,cloud_cover,uv_index,wind_speed_10m,wind_gusts_10m,wind_direction_10m",
			"minutely_15":          "precipitation,snowfall",
			"forecast_minutely_15": "48",
			"daily":                "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,uv_index_max,wind_gusts_10m_max",
			"forecast_days":        "4",
			"hourly":               "temperature_2m",
			"timeformat":           "unixtime",
		}
		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, q.Get(k), v)
			}
		}
		_, _ = w.Write([]byte(openMeteoFacesBody))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.OpenMeteoBase = srv.URL
	obs, err := wf.FetchOpenMeteo(t.Context(), Config{Latitude: 52.5, Longitude: 13.4})
	if err != nil {
		t.Fatal(err)
	}
	n := obs.Now
	if !approx(n.Cloud, 94) || !approx(n.UV, 2.6) || !approx(n.WindKmh, 21.2) || !approx(n.GustKmh, 38.4) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v", n)
	}
	if obs.PrecipStep != 15*time.Minute || len(obs.Precip) != 3 {
		t.Fatalf("precip step %v steps %+v, want 15m and 3 steps (the null one left out)", obs.PrecipStep, obs.Precip)
	}
	if p := obs.Precip[1]; p.At.Unix() != 1782036000 || math.Abs(p.MMh-1.2) > 1e-9 || p.Snow {
		t.Errorf("step 1 = %+v, want 1.2 mm/h rain (0.3 mm in the 15 min before 1782036900)", p)
	}
	if p := obs.Precip[2]; p.At.Unix() != 1782037800 || math.Abs(p.MMh-2) > 1e-9 || !p.Snow {
		t.Errorf("step 2 = %+v, want 2 mm/h snow starting 15 min before its 1782038700 stamp", p)
	}
	if len(obs.Days) != 2 {
		t.Fatalf("days = %+v, want 2", obs.Days)
	}
	d0, d1 := obs.Days[0], obs.Days[1]
	if d0.Start.Unix() != 1781992800 || d0.Cond != render.WeatherRain || d0.Code != "61" || !approx(d0.MinC, 9.1) || !approx(d0.MaxC, 14.2) ||
		!approx(d0.PrecipMM, 2.1) || !approx(d0.Pop, 83) || !approx(d0.UVMax, 4.1) || !approx(d0.GustMaxKmh, 41) {
		t.Errorf("day 0 = %+v", d0)
	}
	if d1.Cond != render.WeatherClouds || d1.Code != "3" || d1.Pop != nil || d1.UVMax != nil {
		t.Errorf("day 1 = %+v, want clouds with pop and uv left out", d1)
	}
}

func TestFetchMetNoUsesCompleteAndAggregatesDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/locationforecast/2.0/complete") {
			t.Errorf("path = %s, want the complete product", r.URL.Path)
		}
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.MetNoBase = srv.URL
	res, err := wf.fetchMetNo(t.Context(), Config{Latitude: 59.9, Longitude: 10.7}, FetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	obs := res.Obs
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Errorf("tz = (%v, %d), want the hint's 7200", obs.TZKnown, obs.TZOffsetSeconds)
	}
	n := obs.Now
	if !approx(n.Cloud, 80) || !approx(n.UV, 0.5) || !approx(n.WindKmh, 18) || !approx(n.GustKmh, 36) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v, want m/s turned into km/h", n)
	}
	if obs.PrecipStep != time.Hour || len(obs.Precip) != 2 {
		t.Fatalf("precip = %v %+v, want the two leading 1-h steps", obs.PrecipStep, obs.Precip)
	}
	if p := obs.Precip[1]; p.MMh != 1 || !p.Snow {
		t.Errorf("sleet step = %+v, want 1 mm/h snow kind", p)
	}
	if len(obs.Days) != 2 {
		t.Fatalf("days = %+v, want 2 local days", obs.Days)
	}
	a, b := obs.Days[0], obs.Days[1]
	if a.Start != time.Date(2026, 6, 20, 22, 0, 0, 0, time.UTC) || a.Cond != render.WeatherSnow || a.Code != "lightsleet" ||
		!approx(a.MinC, 13) || !approx(a.MaxC, 14) || !approx(a.PrecipMM, 1.5) || a.Pop != nil || !approx(a.UVMax, 0.5) || !approx(a.GustMaxKmh, 43.2) {
		t.Errorf("day 0 = %+v", a)
	}
	if b.Start != time.Date(2026, 6, 21, 22, 0, 0, 0, time.UTC) || b.Cond != render.WeatherStorm || b.Code != "heavyrainandthunder" ||
		!approx(b.MinC, 7) || !approx(b.MaxC, 18) || b.PrecipMM == nil || math.Abs(*b.PrecipMM-4.2) > 1e-9 || !approx(b.Pop, 70) {
		t.Errorf("day 1 = %+v", b)
	}
}

func TestMetDaysSplitAPeriodAcrossLocalMidnight(t *testing.T) {
	var ts []metEntry
	six := func(at string, temp float64, sym string, mm, lo, hi, pop float64) string {
		return fmt.Sprintf(`{"time":"%s","data":{"instant":{"details":{"air_temperature":%v}},
"next_6_hours":{"summary":{"symbol_code":"%s"},"details":{"precipitation_amount":%v,"air_temperature_min":%v,"air_temperature_max":%v,"probability_of_precipitation":%v}}}}`,
			at, temp, sym, mm, lo, hi, pop)
	}
	body := "[" + strings.Join([]string{
		six("2026-06-21T12:00:00Z", 18, "rain", 6, 10, 20, 50),
		six("2026-06-21T18:00:00Z", 15, "rain", 3, 5, 12, 80),
		six("2026-06-22T00:00:00Z", 9, "cloudy", 0, 8, 9, 10),
		`{"time":"2026-06-22T06:00:00Z","data":{"instant":{"details":{"air_temperature":10}}}}`,
	}, ",") + "]"
	if err := json.Unmarshal([]byte(body), &ts); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name             string
		off              time.Duration
		precip0, precip1 float64
	}{
		{"UTC+2, 20:00-02:00 is 4 h + 2 h", 2 * time.Hour, 8, 1},
		{"UTC+3, 21:00-03:00 is 3 h + 3 h", 3 * time.Hour, 7.5, 1.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			days := metDays(ts, dayClock{off: c.off})
			if len(days) != 2 {
				t.Fatalf("days = %+v, want 2", days)
			}
			a, b := days[0], days[1]
			if !approx(a.PrecipMM, c.precip0) || !approx(a.MinC, 5) || !approx(a.MaxC, 20) || !approx(a.Pop, 80) || a.Cond != render.WeatherRain {
				t.Errorf("day 0 = precip %v min %v max %v pop %v %s; want %v mm, 5..20 (the crossing period's 5..12 included), 80 %%, rain",
					valueOr(a.PrecipMM, -1), valueOr(a.MinC, -1), valueOr(a.MaxC, -1), valueOr(a.Pop, -1), a.Cond, c.precip0)
			}
			if !approx(b.PrecipMM, c.precip1) || !approx(b.MinC, 8) || !approx(b.MaxC, 10) || !approx(b.Pop, 80) || b.Cond != render.WeatherRain {
				t.Errorf("day 1 = precip %v min %v max %v pop %v %s; want %v mm, 8..10 (its own period and instants, not the evening's 5..12), 80 %%, rain",
					valueOr(b.PrecipMM, -1), valueOr(b.MinC, -1), valueOr(b.MaxC, -1), valueOr(b.Pop, -1), b.Cond, c.precip1)
			}
		})
	}
}

func TestFetchMetNoWithoutOffsetFallsBackToLongitudeDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(metCompleteBody))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.MetNoBase = srv.URL
	res, err := wf.fetchMetNo(t.Context(), Config{Latitude: 59.9, Longitude: 10.7}, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Obs.TZKnown || len(res.Obs.Days) == 0 || res.Obs.Days[0].Start != time.Date(2026, 6, 20, 23, 0, 0, 0, time.UTC) {
		t.Fatalf("tz known %v, days %+v, want day 0 at local midnight for UTC+1 (lon 10.7)", res.Obs.TZKnown, res.Obs.Days)
	}
}

func TestFetchAirQualityReadsUTCOffset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"utc_offset_seconds":7200,"current":{"european_aqi":18}}`))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.AirQualityBase = srv.URL
	obs, err := wf.FetchAirQuality(t.Context(), Config{Latitude: 59.9, Longitude: 10.7})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Fatalf("air tz = (%v, %d), want (true, 7200)", obs.TZKnown, obs.TZOffsetSeconds)
	}
}

func TestMetDaysKeepAPeriodsExtremaWhenTheGapEndsBeforeItsMidpoint(t *testing.T) {
	body := `[
{"time":"2026-06-21T22:00:00Z","data":{"instant":{"details":{"air_temperature":10}},
 "next_6_hours":{"summary":{"symbol_code":"cloudy"},"details":{"air_temperature_min":3,"air_temperature_max":25}}}},
{"time":"2026-06-22T00:00:00Z","data":{"instant":{"details":{"air_temperature":11}},
 "next_6_hours":{"summary":{"symbol_code":"cloudy"},"details":{"air_temperature_min":9,"air_temperature_max":12}}}}
]`
	var ts []metEntry
	if err := json.Unmarshal([]byte(body), &ts); err != nil {
		t.Fatal(err)
	}
	days := metDays(ts, dayClock{})
	if len(days) != 2 {
		t.Fatalf("days = %+v, want 2", days)
	}
	if a := days[0]; !approx(a.MinC, 3) || !approx(a.MaxC, 25) {
		t.Errorf("day 0 = min %v max %v, want 3..25: the 22:00 period's min/max on the only day its 2-h gap covers",
			valueOr(a.MinC, -1), valueOr(a.MaxC, -1))
	}
	if b := days[1]; !approx(b.MinC, 9) || !approx(b.MaxC, 12) {
		t.Errorf("day 1 = min %v max %v, want 9..12: its own period only", valueOr(b.MinC, -1), valueOr(b.MaxC, -1))
	}
}
