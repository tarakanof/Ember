package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	precipStepsOpenMeteo = 48
	precipStepsMetNo     = 14
	msToKmh              = 3.6
)

type weatherFetcher struct {
	client         *http.Client
	openMeteoBase  string
	metNoBase      string
	airQualityBase string
	userAgent      string
}

func newWeatherFetcher() *weatherFetcher {
	return &weatherFetcher{
		client:         &http.Client{Timeout: 12 * time.Second},
		openMeteoBase:  "https://api.open-meteo.com",
		metNoBase:      "https://api.met.no",
		airQualityBase: "https://air-quality-api.open-meteo.com",
		userAgent:      "ember-weather/0.1 (github.com/tarakanof/ember)",
	}
}

type fetchHint struct {
	LastModified    string
	TZOffsetSeconds int
	TZKnown         bool
}

type fetchResult struct {
	Obs          weatherObservation
	LastModified string
	Expires      time.Time
	NotModified  bool
}

type httpMeta struct {
	notModified  bool
	lastModified string
	expires      time.Time
}

func (wf *weatherFetcher) fetch(ctx context.Context, cfg WeatherConfig, hint fetchHint) (fetchResult, error) {
	switch cfg.Provider {
	case "met-no":
		return wf.fetchMetNo(ctx, cfg, hint)
	default:
		obs, err := wf.fetchOpenMeteo(ctx, cfg)
		return fetchResult{Obs: obs}, err
	}
}

func (wf *weatherFetcher) getJSON(ctx context.Context, url, ifModifiedSince string, out any) (httpMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return httpMeta{}, err
	}
	req.Header.Set("User-Agent", wf.userAgent)
	req.Header.Set("Accept", "application/json")
	if ifModifiedSince != "" {
		req.Header.Set("If-Modified-Since", ifModifiedSince)
	}
	resp, err := wf.client.Do(req)
	if err != nil {
		return httpMeta{}, err
	}
	defer resp.Body.Close()
	meta := httpMeta{lastModified: resp.Header.Get("Last-Modified")}
	if t, err := http.ParseTime(resp.Header.Get("Expires")); err == nil {
		meta.expires = t
	}
	if resp.StatusCode == http.StatusNotModified && ifModifiedSince != "" {
		meta.notModified = true
		return meta, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return httpMeta{}, fmt.Errorf("weather http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return meta, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func at[T any](s []*T, i int) *T {
	if i < len(s) {
		return s[i]
	}
	return nil
}

func scaled(v *float64, k float64) *float64 {
	if v == nil {
		return nil
	}
	return new(*v * k)
}

func (wf *weatherFetcher) fetchOpenMeteo(ctx context.Context, cfg WeatherConfig) (weatherObservation, error) {
	url := fmt.Sprintf("%s/v1/forecast?latitude=%.4f&longitude=%.4f"+
		"&current=temperature_2m,weather_code,cloud_cover,uv_index,wind_speed_10m,wind_gusts_10m,wind_direction_10m"+
		"&minutely_15=precipitation,snowfall&forecast_minutely_15=%d"+
		"&hourly=temperature_2m&forecast_hours=%d"+
		"&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,uv_index_max,wind_gusts_10m_max&forecast_days=%d"+
		"&timezone=auto&timeformat=unixtime",
		strings.TrimRight(wf.openMeteoBase, "/"), cfg.Latitude, cfg.Longitude, precipStepsOpenMeteo, forecastFetchHours, forecastDays)
	var body struct {
		UTCOffsetSeconds int `json:"utc_offset_seconds"`
		Current          struct {
			Time        int64    `json:"time"`
			Temperature float64  `json:"temperature_2m"`
			WeatherCode int      `json:"weather_code"`
			Cloud       *float64 `json:"cloud_cover"`
			UV          *float64 `json:"uv_index"`
			Wind        *float64 `json:"wind_speed_10m"`
			Gust        *float64 `json:"wind_gusts_10m"`
			WindDir     *float64 `json:"wind_direction_10m"`
		} `json:"current"`
		Minutely15 struct {
			Time          []int64    `json:"time"`
			Precipitation []*float64 `json:"precipitation"`
			Snowfall      []*float64 `json:"snowfall"`
		} `json:"minutely_15"`
		Hourly struct {
			Time        []int64   `json:"time"`
			Temperature []float64 `json:"temperature_2m"`
		} `json:"hourly"`
		Daily struct {
			Time        []int64    `json:"time"`
			WeatherCode []*int     `json:"weather_code"`
			MaxC        []*float64 `json:"temperature_2m_max"`
			MinC        []*float64 `json:"temperature_2m_min"`
			PrecipMM    []*float64 `json:"precipitation_sum"`
			Pop         []*float64 `json:"precipitation_probability_max"`
			UVMax       []*float64 `json:"uv_index_max"`
			GustMax     []*float64 `json:"wind_gusts_10m_max"`
		} `json:"daily"`
	}
	if _, err := wf.getJSON(ctx, url, "", &body); err != nil {
		return weatherObservation{}, err
	}
	var start time.Time
	if len(body.Hourly.Time) > 0 {
		start = time.Unix(body.Hourly.Time[0], 0)
	}
	var observed time.Time
	if body.Current.Time > 0 {
		observed = time.Unix(body.Current.Time, 0).UTC()
	}
	cond, severe := wmoCondition(body.Current.WeatherCode)
	hourly := body.Hourly.Temperature
	if len(hourly) > forecastFetchHours {
		hourly = hourly[:forecastFetchHours]
	}
	c := body.Current
	obs := weatherObservation{
		Condition: cond, ConditionCode: strconv.Itoa(body.Current.WeatherCode),
		TempC: body.Current.Temperature, Severe: severe, ObservedAt: observed, Hourly: hourly, HourlyStart: start,
		Overlay:         wmoOverlay(body.Current.WeatherCode),
		TZOffsetSeconds: body.UTCOffsetSeconds, TZKnown: true,
		Now:        weatherNow{Cloud: c.Cloud, UV: c.UV, WindKmh: c.Wind, GustKmh: c.Gust, WindDir: c.WindDir},
		PrecipStep: 15 * time.Minute,
	}
	m := body.Minutely15
	for i, t := range m.Time {
		p := at(m.Precipitation, i)
		if p == nil {
			continue
		}
		snow := at(m.Snowfall, i)
		start := time.Unix(t, 0).UTC().Add(-obs.PrecipStep)
		obs.Precip = append(obs.Precip, precipStep{At: start, MMh: *p * 4, Snow: snow != nil && *snow > 0})
		if len(obs.Precip) == precipStepsOpenMeteo {
			break
		}
	}
	d := body.Daily
	for i, t := range d.Time {
		if i == forecastDays {
			break
		}
		day := weatherDay{Start: time.Unix(t, 0).UTC(), MinC: at(d.MinC, i), MaxC: at(d.MaxC, i),
			PrecipMM: at(d.PrecipMM, i), Pop: at(d.Pop, i), UVMax: at(d.UVMax, i), GustMaxKmh: at(d.GustMax, i)}
		if code := at(d.WeatherCode, i); code != nil {
			day.Cond, _ = wmoCondition(*code)
			day.Code = strconv.Itoa(*code)
		}
		obs.Days = append(obs.Days, day)
	}
	return obs, nil
}

type metPeriod struct {
	Summary struct {
		SymbolCode string `json:"symbol_code"`
	} `json:"summary"`
	Details struct {
		PrecipMM *float64 `json:"precipitation_amount"`
		Pop      *float64 `json:"probability_of_precipitation"`
		MaxC     *float64 `json:"air_temperature_max"`
		MinC     *float64 `json:"air_temperature_min"`
	} `json:"details"`
}

type metEntry struct {
	Time time.Time `json:"time"`
	Data struct {
		Instant struct {
			Details struct {
				AirTemperature float64  `json:"air_temperature"`
				Cloud          *float64 `json:"cloud_area_fraction"`
				UV             *float64 `json:"ultraviolet_index_clear_sky"`
				Wind           *float64 `json:"wind_speed"`
				Gust           *float64 `json:"wind_speed_of_gust"`
				WindFrom       *float64 `json:"wind_from_direction"`
			} `json:"details"`
		} `json:"instant"`
		Next1Hours *metPeriod `json:"next_1_hours"`
		Next6Hours *metPeriod `json:"next_6_hours"`
	} `json:"data"`
}

func (e metEntry) symbol() string {
	if e.Data.Next1Hours != nil {
		return e.Data.Next1Hours.Summary.SymbolCode
	}
	return ""
}

func (wf *weatherFetcher) fetchMetNo(ctx context.Context, cfg WeatherConfig, hint fetchHint) (fetchResult, error) {
	url := fmt.Sprintf("%s/weatherapi/locationforecast/2.0/complete?lat=%.4f&lon=%.4f",
		strings.TrimRight(wf.metNoBase, "/"), cfg.Latitude, cfg.Longitude)
	var body struct {
		Properties struct {
			Timeseries []metEntry `json:"timeseries"`
		} `json:"properties"`
	}
	meta, err := wf.getJSON(ctx, url, hint.LastModified, &body)
	if err != nil {
		return fetchResult{}, err
	}
	res := fetchResult{LastModified: meta.lastModified, Expires: meta.expires, NotModified: meta.notModified}
	if res.NotModified {
		return res, nil
	}
	ts := body.Properties.Timeseries
	if len(ts) == 0 {
		return fetchResult{}, errors.New("met-no: empty timeseries")
	}
	first := ts[0]
	sym := first.symbol()
	cond, severe := metSymbolCondition(sym)
	n := min(len(ts), forecastFetchHours)
	hourly := make([]float64, n)
	for i := 0; i < n; i++ {
		hourly[i] = ts[i].Data.Instant.Details.AirTemperature
	}
	in := first.Data.Instant.Details
	off := localOffset(weatherObservation{TZOffsetSeconds: hint.TZOffsetSeconds, TZKnown: hint.TZKnown}, cfg.Longitude)
	res.Obs = weatherObservation{
		Condition: cond, ConditionCode: sym,
		TempC: in.AirTemperature, Severe: severe, ObservedAt: first.Time.UTC(),
		Hourly: hourly, HourlyStart: first.Time,
		Overlay:         metSymbolOverlay(sym),
		TZOffsetSeconds: hint.TZOffsetSeconds, TZKnown: hint.TZKnown,
		ValidUntil: meta.expires,
		Now: weatherNow{Cloud: in.Cloud, UV: in.UV, WindKmh: scaled(in.Wind, msToKmh),
			GustKmh: scaled(in.Gust, msToKmh), WindDir: in.WindFrom},
		PrecipStep: time.Hour,
		Precip:     metPrecip(ts),
		Days:       metDays(ts, off),
	}
	return res, nil
}

func metInterval(ts []metEntry, i int) time.Duration {
	if i+1 < len(ts) {
		return ts[i+1].Time.Sub(ts[i].Time)
	}
	if ts[i].Data.Next6Hours == nil {
		return time.Hour
	}
	return 6 * time.Hour
}

func metPrecip(ts []metEntry) []precipStep {
	var out []precipStep
	for i, e := range ts {
		p := e.Data.Next1Hours
		if metInterval(ts, i) != time.Hour || p == nil || len(out) == precipStepsMetNo {
			break
		}
		if p.Details.PrecipMM == nil {
			continue
		}
		cond, _ := metSymbolCondition(p.Summary.SymbolCode)
		out = append(out, precipStep{At: e.Time.UTC(), MMh: *p.Details.PrecipMM, Snow: cond == render.WeatherSnow})
	}
	return out
}

var weatherCondRank = map[string]int{
	render.WeatherClear: 0, render.WeatherClouds: 1, render.WeatherFog: 2,
	render.WeatherRain: 3, render.WeatherSnow: 4, render.WeatherStorm: 5,
}

func maxOpt(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur >= *v) {
		return cur
	}
	return new(*v)
}

func minOpt(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur <= *v) {
		return cur
	}
	return new(*v)
}

func metDays(ts []metEntry, off time.Duration) []weatherDay {
	var days []weatherDay
	for i, e := range ts {
		y, m, d := e.Time.UTC().Add(off).Date()
		start := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-off)
		if len(days) == 0 || !days[len(days)-1].Start.Equal(start) {
			if len(days) == forecastDays {
				break
			}
			days = append(days, weatherDay{Start: start})
		}
		day := &days[len(days)-1]
		in := e.Data.Instant.Details
		day.MinC = minOpt(day.MinC, &in.AirTemperature)
		day.MaxC = maxOpt(day.MaxC, &in.AirTemperature)
		day.UVMax = maxOpt(day.UVMax, in.UV)
		day.GustMaxKmh = maxOpt(day.GustMaxKmh, scaled(in.Gust, msToKmh))
		step := metInterval(ts, i)
		p, share := e.Data.Next1Hours, 1.0
		if step > time.Hour {
			p, share = e.Data.Next6Hours, min(1, step.Hours()/6)
			if p != nil {
				day.MinC = minOpt(day.MinC, p.Details.MinC)
				day.MaxC = maxOpt(day.MaxC, p.Details.MaxC)
			}
		}
		if p == nil {
			continue
		}
		day.Pop = maxOpt(day.Pop, p.Details.Pop)
		if p.Details.PrecipMM != nil {
			day.PrecipMM = new(*p.Details.PrecipMM*share + valueOr(day.PrecipMM, 0))
		}
		if sym := p.Summary.SymbolCode; sym != "" {
			cond, _ := metSymbolCondition(sym)
			if day.Code == "" || weatherCondRank[cond] > weatherCondRank[day.Cond] {
				day.Cond, day.Code = cond, sym
			}
		}
	}
	return days
}

func valueOr(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

func (wf *weatherFetcher) fetchAirQuality(ctx context.Context, cfg WeatherConfig) (airObservation, error) {
	url := fmt.Sprintf("%s/v1/air-quality?latitude=%.4f&longitude=%.4f&current=european_aqi,pm2_5,pm10&hourly=european_aqi&forecast_hours=%d&timezone=auto&timeformat=unixtime",
		strings.TrimRight(wf.airQualityBase, "/"), cfg.Latitude, cfg.Longitude, airFetchHours)
	var body struct {
		UTCOffsetSeconds *int `json:"utc_offset_seconds"`
		Current          struct {
			AQI  float64 `json:"european_aqi"`
			PM25 float64 `json:"pm2_5"`
			PM10 float64 `json:"pm10"`
		} `json:"current"`
		Hourly struct {
			Time []int64   `json:"time"`
			AQI  []float64 `json:"european_aqi"`
		} `json:"hourly"`
	}
	if _, err := wf.getJSON(ctx, url, "", &body); err != nil {
		return airObservation{}, err
	}
	hourly := body.Hourly.AQI
	if len(hourly) > airFetchHours {
		hourly = hourly[:airFetchHours]
	}
	var start time.Time
	if len(body.Hourly.Time) > 0 {
		start = time.Unix(body.Hourly.Time[0], 0)
	}
	obs := airObservation{AQI: body.Current.AQI, PM25: body.Current.PM25, PM10: body.Current.PM10, HourlyAQI: hourly, HourlyStart: start}
	if body.UTCOffsetSeconds != nil {
		obs.TZOffsetSeconds, obs.TZKnown = *body.UTCOffsetSeconds, true
	}
	return obs, nil
}
