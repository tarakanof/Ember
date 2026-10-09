package main

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handleWeatherPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherPreview(r.URL.Query(), time.Now()))
}

func (a *App) weatherPreview(q url.Values, now time.Time) render.Preview {
	cfg := a.cfg.Load().Weather
	cfg.RotateInApps = boolPtr(queryBoolDefault(q.Get("rotate_in_apps"), cfg.RotateInAppsEnabled()))
	cfg.ForecastTile = boolPtr(queryBoolDefault(q.Get("forecast_tile"), cfg.ForecastTileEnabled()))
	cfg.AirTile = boolPtr(queryBoolDefault(q.Get("air_tile"), cfg.AirTileEnabled()))
	if v, err := strconv.Atoi(strings.TrimSpace(q.Get("forecast_hours"))); err == nil {
		cfg.ForecastHours = v
	}
	if u := strings.TrimSpace(q.Get("units")); u == "metric" || u == "imperial" {
		cfg.Units = u
	}
	cfg.MoonPhase = boolPtr(queryBoolDefault(q.Get("moon_phase"), cfg.MoonPhaseEnabled()))
	if lat, lon, ok := queryLatLon(q); ok {
		cfg.Latitude, cfg.Longitude = lat, lon
	}

	wx := a.weather.state(cfg, now)
	if !wx.HaveObs {
		wx.Obs = sampleWeatherObservation(now)
	}
	if !wx.HaveAir {
		wx.Air = sampleAirObservation(now)
	}
	in := tileInputs{now: now, weather: cfg, wx: wx}
	return previewTiles(in, weatherTile.card, forecastTile.card, airTile.card)
}

func queryLatLon(q url.Values) (lat, lon float64, ok bool) {
	lat, errLat := strconv.ParseFloat(strings.TrimSpace(q.Get("lat")), 64)
	lon, errLon := strconv.ParseFloat(strings.TrimSpace(q.Get("lon")), 64)
	if errLat != nil || errLon != nil || math.IsNaN(lat) || math.IsNaN(lon) ||
		lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

func sampleAirObservation(now time.Time) airObservation {
	hourly := make([]float64, 24)
	for i := range hourly {
		hourly[i] = 42 - 24*math.Sin(float64(i)/24*math.Pi)
	}
	return airObservation{AQI: 42, PM25: 12, PM10: 19, HourlyAQI: hourly, FetchedAt: now}
}

func sampleWeatherObservation(now time.Time) weatherObservation {
	hourly := make([]float64, 24)
	for i := range hourly {
		hourly[i] = 16 + 6*math.Sin((float64(i)-3)/24*2*math.Pi)
	}
	return weatherObservation{Condition: render.WeatherClouds, TempC: 21, Hourly: hourly, FetchedAt: now}
}
