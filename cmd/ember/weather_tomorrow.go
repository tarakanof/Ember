package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	tomorrowIO          = "tomorrow-io"
	tomorrowKeyEnv      = "EMBER_TOMORROW_API_KEY"
	precipStepsTomorrow = 48
	tomorrowPrecipStep  = 15 * time.Minute
)

var errTomorrowNoKey = errors.New("tomorrow-io: " + tomorrowKeyEnv + " is not set on the server")

type tomorrowValues struct {
	Temperature           *float64 `json:"temperature"`
	WeatherCode           *int     `json:"weatherCode"`
	CloudCover            *float64 `json:"cloudCover"`
	UVIndex               *float64 `json:"uvIndex"`
	WindSpeed             *float64 `json:"windSpeed"`
	WindGust              *float64 `json:"windGust"`
	WindDirection         *float64 `json:"windDirection"`
	RainIntensity         *float64 `json:"rainIntensity"`
	SnowIntensity         *float64 `json:"snowIntensity"`
	SleetIntensity        *float64 `json:"sleetIntensity"`
	FreezingRainIntensity *float64 `json:"freezingRainIntensity"`
}

type tomorrowEntry struct {
	Time   time.Time      `json:"time"`
	Values tomorrowValues `json:"values"`
}

type tomorrowDay struct {
	Time   time.Time `json:"time"`
	Values struct {
		TemperatureMax              *float64 `json:"temperatureMax"`
		TemperatureMin              *float64 `json:"temperatureMin"`
		PrecipitationProbabilityMax *float64 `json:"precipitationProbabilityMax"`
		RainAccumulationSum         *float64 `json:"rainAccumulationSum"`
		SnowAccumulationLweSum      *float64 `json:"snowAccumulationLweSum"`
		SleetAccumulationLweSum     *float64 `json:"sleetAccumulationLweSum"`
		IceAccumulationLweSum       *float64 `json:"iceAccumulationLweSum"`
		UVIndexMax                  *float64 `json:"uvIndexMax"`
		WindGustMax                 *float64 `json:"windGustMax"`
		WeatherCodeMax              *int     `json:"weatherCodeMax"`
	} `json:"values"`
}

func (wf *weatherFetcher) fetchTomorrow(ctx context.Context, cfg WeatherConfig, hint fetchHint) (weatherObservation, error) {
	if wf.tomorrowKey == "" {
		return weatherObservation{}, errTomorrowNoKey
	}
	u := fmt.Sprintf("%s/v4/weather/forecast?location=%.4f,%.4f&units=metric&apikey=%s",
		strings.TrimRight(wf.tomorrowBase, "/"), cfg.Latitude, cfg.Longitude, url.QueryEscape(wf.tomorrowKey))
	var body struct {
		Timelines struct {
			Minutely []tomorrowEntry `json:"minutely"`
			Hourly   []tomorrowEntry `json:"hourly"`
			Daily    []tomorrowDay   `json:"daily"`
		} `json:"timelines"`
	}
	if _, err := wf.getJSON(ctx, u, "", &body); err != nil {
		return weatherObservation{}, wf.tomorrowError(ctx, err, time.Now())
	}
	tl := body.Timelines
	if len(tl.Hourly) == 0 && len(tl.Minutely) == 0 {
		return weatherObservation{}, errors.New("tomorrow-io: empty timelines")
	}
	steps := tomorrowSteps(tl.Minutely, tl.Hourly)
	obs := weatherObservation{
		TZOffsetSeconds: hint.TZOffsetSeconds, TZKnown: hint.TZKnown,
		PrecipStep: tomorrowPrecipStep,
		Precip:     tomorrowPrecip(tl.Minutely, tl.Hourly),
		Steps:      steps,
	}
	if len(tl.Hourly) > 0 {
		n := min(len(tl.Hourly), forecastFetchHours)
		obs.HourlyStart = tl.Hourly[0].Time.UTC()
		obs.Hourly = make([]float64, n)
		for i := range n {
			obs.Hourly[i] = valueOr(tl.Hourly[i].Values.Temperature, 0)
		}
	}
	var from time.Time
	if len(steps) > 0 {
		from = steps[0].At
	}
	obs.Days = tomorrowDays(tl.Daily, tl.Hourly, localOffset(obs, cfg.Longitude), from)
	if len(steps) > 0 {
		obs = obs.current(steps[0].At)
	}
	return obs, nil
}

var (
	errTomorrowRequest  = errors.New("tomorrow-io: request failed")
	errTomorrowResponse = errors.New("tomorrow-io: invalid response")
)

func (wf *weatherFetcher) tomorrowError(ctx context.Context, err error, now time.Time) error {
	return keyedFetchError(ctx, err, now, errTomorrowRequest, errTomorrowResponse)
}

func tomorrowSteps(minutely, hourly []tomorrowEntry) []currentStep {
	var steps []currentStep
	var first time.Time
	if len(minutely) > 0 {
		steps = append(steps, minutely[0].current())
		first = minutely[0].Time
	}
	for _, e := range hourly {
		if len(steps) == forecastFetchHours {
			break
		}
		if !first.IsZero() && !e.Time.After(first) {
			continue
		}
		steps = append(steps, e.current())
	}
	return steps
}

func (e tomorrowEntry) current() currentStep {
	v := e.Values
	s := currentStep{At: e.Time.UTC(), TempC: valueOr(v.Temperature, 0), Condition: render.WeatherClouds,
		Now: weatherNow{Cloud: v.CloudCover, UV: v.UVIndex, WindKmh: scaled(v.WindSpeed, msToKmh),
			GustKmh: scaled(v.WindGust, msToKmh), WindDir: v.WindDirection}}
	if v.WeatherCode != nil {
		if wmo, ok := tomorrowWMO(*v.WeatherCode); ok {
			s.Condition, s.Severe = wmoCondition(wmo)
			s.Code = strconv.Itoa(wmo)
			s.Overlay = wmoOverlay(wmo)
		}
	}
	return s
}

func (v tomorrowValues) precip() (mmh float64, snow, ok bool) {
	var liquid, frozen float64
	for _, p := range []*float64{v.RainIntensity, v.FreezingRainIntensity} {
		if p != nil {
			liquid += *p
			ok = true
		}
	}
	for _, p := range []*float64{v.SnowIntensity, v.SleetIntensity} {
		if p != nil {
			frozen += *p
			ok = true
		}
	}
	return liquid + frozen, frozen > liquid, ok
}

func tomorrowPrecip(minutely, hourly []tomorrowEntry) []precipStep {
	var out []precipStep
	type block struct {
		sum, frozen float64
		n           int
	}
	var starts []time.Time
	blocks := map[time.Time]*block{}
	for _, e := range minutely {
		mmh, snow, ok := e.Values.precip()
		if !ok {
			continue
		}
		at := e.Time.UTC().Truncate(tomorrowPrecipStep)
		b := blocks[at]
		if b == nil {
			b = &block{}
			blocks[at] = b
			starts = append(starts, at)
		}
		b.sum += mmh
		b.n++
		if snow {
			b.frozen++
		}
	}
	slices.SortFunc(starts, func(a, b time.Time) int { return a.Compare(b) })
	var covered time.Time
	for _, at := range starts {
		if len(out) == precipStepsTomorrow {
			return out
		}
		b := blocks[at]
		out = append(out, precipStep{At: at, MMh: b.sum / float64(b.n), Snow: b.frozen*2 > float64(b.n)})
		covered = at.Add(tomorrowPrecipStep)
	}
	for _, e := range hourly {
		mmh, snow, ok := e.Values.precip()
		if !ok {
			continue
		}
		for q := range time.Hour / tomorrowPrecipStep {
			at := e.Time.UTC().Add(q * tomorrowPrecipStep)
			if at.Before(covered) {
				continue
			}
			if len(out) == precipStepsTomorrow {
				return out
			}
			out = append(out, precipStep{At: at, MMh: mmh, Snow: snow})
		}
	}
	return out
}

func tomorrowDays(daily []tomorrowDay, hourly []tomorrowEntry, off time.Duration, from time.Time) []weatherDay {
	var days []weatherDay
	for _, d := range daily {
		if len(days) == forecastDays {
			break
		}
		y, m, dd := d.Time.UTC().Add(off).Date()
		start := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC).Add(-off)
		if !from.IsZero() && !start.Add(24*time.Hour).After(from) {
			continue
		}
		if len(days) > 0 && !start.After(days[len(days)-1].Start) {
			continue
		}
		v := d.Values
		day := weatherDay{Start: start, MinC: v.TemperatureMin, MaxC: v.TemperatureMax, Pop: v.PrecipitationProbabilityMax,
			UVMax: v.UVIndexMax, GustMaxKmh: scaled(v.WindGustMax, msToKmh)}
		for _, p := range []*float64{v.RainAccumulationSum, v.SnowAccumulationLweSum, v.SleetAccumulationLweSum, v.IceAccumulationLweSum} {
			if p != nil {
				day.PrecipMM = new(*p + valueOr(day.PrecipMM, 0))
			}
		}
		if wmo := tomorrowDayCode(hourly, d.Time.UTC(), from, v.WeatherCodeMax); wmo != nil {
			day.Cond, _ = wmoCondition(*wmo)
			day.Code = strconv.Itoa(*wmo)
		}
		days = append(days, day)
	}
	return days
}

var tomorrowWMOCodes = map[int]int{
	1000: 0, 1100: 1, 1101: 2, 1102: 3, 1001: 3,
	2000: 45, 2100: 45,
	4000: 53, 4200: 61, 4001: 63, 4201: 65,
	5001: 71, 5100: 71, 5000: 73, 5101: 75,
	6000: 56, 6200: 66, 6001: 66, 6201: 67,
	7102: 77, 7000: 77, 7101: 77,
	8000: 95,
}

func tomorrowDayCode(hourly []tomorrowEntry, start, now time.Time, codeMax *int) *int {
	end := start.Add(24 * time.Hour)
	hours := map[int]int{}
	future := 0
	for _, e := range hourly {
		if e.Values.WeatherCode == nil || e.Time.Before(start) || !e.Time.Before(end) {
			continue
		}
		if wmo, ok := tomorrowWMO(*e.Values.WeatherCode); ok {
			hours[wmo]++
		}
		if !e.Time.Before(now.Truncate(time.Hour)) {
			future++
		}
	}
	if wmo, ok := tomorrowWMO(valueOrInt(codeMax, -1)); ok {
		open := int(end.Sub(later(start, now.Truncate(time.Hour))) / time.Hour)
		if gap := open - future; gap > 0 {
			hours[wmo] += gap
		}
	}
	return pickDayCode(hours)
}

func tomorrowWMO(code int) (int, bool) {
	wmo, ok := tomorrowWMOCodes[code]
	return wmo, ok
}
