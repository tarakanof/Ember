package weather

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	WeatherAPI            = "weatherapi"
	WeatherAPIKeyEnv      = "EMBER_WEATHERAPI_KEY"
	weatherAPIDays        = 3
	precipStepsWeatherAPI = 48
	weatherAPIPrecipStep  = 15 * time.Minute
)

var (
	errWeatherAPINoKey    = errors.New("weatherapi: " + WeatherAPIKeyEnv + " is not set on the server")
	errWeatherAPIRequest  = errors.New("weatherapi: request failed")
	errWeatherAPIResponse = errors.New("weatherapi: invalid response")
)

type waCondition struct {
	Code *int `json:"code"`
}

type waHour struct {
	TimeEpoch  int64       `json:"time_epoch"`
	TempC      *float64    `json:"temp_c"`
	Condition  waCondition `json:"condition"`
	WindKph    *float64    `json:"wind_kph"`
	WindDegree *float64    `json:"wind_degree"`
	GustKph    *float64    `json:"gust_kph"`
	PrecipMM   *float64    `json:"precip_mm"`
	SnowCM     *float64    `json:"snow_cm"`
	Cloud      *float64    `json:"cloud"`
	UV         *float64    `json:"uv"`
}

type waCurrent struct {
	LastUpdatedEpoch int64       `json:"last_updated_epoch"`
	TempC            *float64    `json:"temp_c"`
	Condition        waCondition `json:"condition"`
	WindKph          *float64    `json:"wind_kph"`
	WindDegree       *float64    `json:"wind_degree"`
	GustKph          *float64    `json:"gust_kph"`
	Cloud            *float64    `json:"cloud"`
	UV               *float64    `json:"uv"`
}

type waDay struct {
	Date string `json:"date"`
	Day  struct {
		MaxtempC          *float64    `json:"maxtemp_c"`
		MintempC          *float64    `json:"mintemp_c"`
		TotalprecipMM     *float64    `json:"totalprecip_mm"`
		DailyChanceOfRain *float64    `json:"daily_chance_of_rain"`
		DailyChanceOfSnow *float64    `json:"daily_chance_of_snow"`
		UV                *float64    `json:"uv"`
		Condition         waCondition `json:"condition"`
	} `json:"day"`
	Hour []waHour `json:"hour"`
}

func (wf *Fetcher) fetchWeatherAPI(ctx context.Context, cfg Config, hint FetchHint) (Observation, error) {
	if wf.WeatherAPIKey == "" {
		return Observation{}, errWeatherAPINoKey
	}
	u := fmt.Sprintf("%s/v1/forecast.json?key=%s&q=%.4f,%.4f&days=%d&aqi=no&alerts=no",
		strings.TrimRight(wf.WeatherAPIBase, "/"), url.QueryEscape(wf.WeatherAPIKey), cfg.Latitude, cfg.Longitude, weatherAPIDays)
	var body struct {
		Location struct {
			TzID string `json:"tz_id"`
		} `json:"location"`
		Current  *waCurrent `json:"current"`
		Forecast struct {
			Forecastday []waDay `json:"forecastday"`
		} `json:"forecast"`
	}
	if _, err := wf.getJSON(ctx, u, "", &body); err != nil {
		return Observation{}, keyedFetchError(ctx, err, time.Now(), errWeatherAPIRequest, errWeatherAPIResponse)
	}
	days := body.Forecast.Forecastday
	if body.Current != nil && body.Current.LastUpdatedEpoch <= 0 {
		body.Current = nil
	}
	var hours []waHour
	for _, d := range days {
		for _, h := range d.Hour {
			if h.TimeEpoch > 0 {
				hours = append(hours, h)
			}
		}
	}
	var from time.Time
	switch {
	case body.Current != nil:
		from = time.Unix(body.Current.LastUpdatedEpoch, 0).UTC()
	case len(hours) > 0:
		from = later(hours[0].at(), wf.clock().UTC())
	default:
		return Observation{}, errWeatherAPIResponse
	}
	obs := Observation{TZOffsetSeconds: hint.TZOffsetSeconds, TZKnown: hint.TZKnown, PrecipStep: weatherAPIPrecipStep}
	loc := loadZone(body.Location.TzID)
	if loc != nil {
		_, obs.TZOffsetSeconds = from.In(loc).Zone()
		obs.TZKnown = true
	}
	clock := dayClock{loc: loc, off: LocalOffset(obs, cfg.Longitude)}
	if body.Current != nil {
		obs.Steps = append(obs.Steps, body.Current.step())
	}
	for _, h := range hours {
		t := h.at()
		if !t.Add(time.Hour).After(from) {
			continue
		}
		if len(obs.Hourly) < forecastFetchHours {
			if len(obs.Hourly) == 0 {
				obs.HourlyStart = t
			}
			obs.Hourly = append(obs.Hourly, valueOr(h.TempC, 0))
		}
		if (body.Current == nil || t.After(from)) && len(obs.Steps) < forecastFetchHours {
			obs.Steps = append(obs.Steps, h.step())
		}
		if h.PrecipMM == nil {
			continue
		}
		snow := h.SnowCM != nil && *h.SnowCM > 0
		if wmo, ok := weatherAPIWMO(valueOrInt(h.Condition.Code, -1)); ok {
			cond, _ := wmoCondition(wmo)
			snow = snow || cond == render.WeatherSnow
		}
		for q := range time.Hour / weatherAPIPrecipStep {
			if len(obs.Precip) == precipStepsWeatherAPI {
				break
			}
			obs.Precip = append(obs.Precip, PrecipStep{At: t.Add(q * weatherAPIPrecipStep), MMh: *h.PrecipMM, Snow: snow})
		}
	}
	if len(obs.Steps) == 0 {
		return Observation{}, errWeatherAPIResponse
	}
	obs.Days = waDays(days, clock, from)
	obs = obs.Current(obs.Steps[0].At)
	return obs, nil
}

func (h waHour) at() time.Time { return time.Unix(h.TimeEpoch, 0).UTC() }

func waStep(at time.Time, temp *float64, c waCondition, now Now) currentStep {
	s := currentStep{At: at, TempC: valueOr(temp, 0), Condition: render.WeatherClouds, Now: now}
	if wmo, ok := weatherAPIWMO(valueOrInt(c.Code, -1)); ok {
		s.Condition, s.Severe = wmoCondition(wmo)
		s.Code = strconv.Itoa(wmo)
		s.Overlay = wmoOverlay(wmo)
	}
	return s
}

func (c waCurrent) step() currentStep {
	return waStep(time.Unix(c.LastUpdatedEpoch, 0).UTC(), c.TempC, c.Condition,
		Now{Cloud: c.Cloud, UV: c.UV, WindKmh: c.WindKph, GustKmh: c.GustKph, WindDir: c.WindDegree})
}

func (h waHour) step() currentStep {
	return waStep(h.at(), h.TempC, h.Condition,
		Now{Cloud: h.Cloud, UV: h.UV, WindKmh: h.WindKph, GustKmh: h.GustKph, WindDir: h.WindDegree})
}

func waDays(days []waDay, clock dayClock, from time.Time) []Day {
	var out []Day
	for _, d := range days {
		if len(out) == forecastDays {
			break
		}
		date, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		start := clock.midnight(date.Date())
		end := clock.after(start)
		if !end.After(from) || (len(out) > 0 && !start.After(out[len(out)-1].Start)) {
			continue
		}
		v := d.Day
		day := Day{Start: start, End: end, MinC: v.MintempC, MaxC: v.MaxtempC, PrecipMM: v.TotalprecipMM,
			Pop: maxOpt(v.DailyChanceOfRain, v.DailyChanceOfSnow), UVMax: v.UV}
		counts := map[int]int{}
		for _, h := range d.Hour {
			day.UVMax = maxOpt(day.UVMax, h.UV)
			day.GustMaxKmh = maxOpt(day.GustMaxKmh, h.GustKph)
			if !h.at().Add(time.Hour).After(from) {
				continue
			}
			if wmo, ok := weatherAPIWMO(valueOrInt(h.Condition.Code, -1)); ok {
				counts[wmo]++
			}
		}
		if len(counts) == 0 {
			if wmo, ok := weatherAPIWMO(valueOrInt(v.Condition.Code, -1)); ok {
				counts[wmo] = 1
			}
		}
		if wmo := pickDayCode(counts); wmo != nil {
			day.Cond, _ = wmoCondition(*wmo)
			day.Code = strconv.Itoa(*wmo)
		}
		out = append(out, day)
	}
	return out
}

var weatherAPIWMOCodes = map[int]int{
	1000: 0, 1003: 2, 1006: 3, 1009: 3,
	1012: 45, 1015: 45, 1018: 45, 1021: 45, 1024: 45, 1027: 45, 1030: 45, 1033: 45,
	1036: 45, 1039: 45, 1042: 45, 1045: 45, 1048: 45, 1135: 45, 1147: 48,
	1063: 80, 1066: 85, 1069: 85, 1072: 56, 1087: 95,
	1114: 73, 1117: 75,
	1150: 51, 1153: 51, 1168: 56, 1171: 57,
	1180: 61, 1183: 61, 1186: 63, 1189: 63, 1192: 65, 1195: 65,
	1198: 66, 1201: 67, 1204: 71, 1207: 73,
	1210: 71, 1213: 71, 1216: 73, 1219: 73, 1222: 75, 1225: 75, 1237: 77,
	1240: 80, 1243: 81, 1246: 82, 1249: 85, 1252: 85, 1255: 85, 1258: 86,
	1261: 77, 1264: 77, 1273: 95, 1276: 95, 1279: 95, 1282: 95,
}

func weatherAPIWMO(code int) (int, bool) {
	wmo, ok := weatherAPIWMOCodes[code]
	return wmo, ok
}
