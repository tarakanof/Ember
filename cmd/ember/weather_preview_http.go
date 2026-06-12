package main

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// handleWeatherPreview renders the weather/forecast tiles under a draft config
// into the same 32×8 frame grids as /v1/preview. Open and read-only. Uses the
// live observation when one exists, else a canned sample so the preview never
// renders blank (before the first fetch, or with the widget disabled).
//
// Query params:
//   - rotate_in_apps   bool (default true)  → "weather" frame
//   - forecast_tile    bool (default true)  → "forecast" frame
//   - air_tile         bool (default true)  → "air" frame
//   - forecast_hours   int 6..24 (default 24)
//   - units            "metric"|"imperial" (default "metric")
//
// tile_native_icons is deliberately not a param: the canvas can't animate
// gallery icons, so the preview always shows the drawn sprite.
func (a *App) handleWeatherPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	obs, have := a.weather.current()
	if !have {
		obs = sampleWeatherObservation(now)
	}
	units := strings.TrimSpace(q.Get("units"))
	if units != "imperial" {
		units = "metric"
	}
	hours := 24
	if v, err := strconv.Atoi(q.Get("forecast_hours")); err == nil {
		hours = v
	}
	if hours < 6 {
		hours = 6
	} else if hours > 24 {
		hours = 24
	}
	tempText := weatherTempText(obs.TempC, units)
	window := forecastWindow(obs.Hourly, hours)

	p := render.Preview{Width: 32, Height: 8, Frames: []render.CardFrame{}}
	if queryBoolDefault(q.Get("rotate_in_apps"), true) {
		f := render.WeatherTileFrame(obs.Condition, tempText, window, nil)
		p.Frames = append(p.Frames, render.CardFrame{Card: "weather", Pixels: render.HexPixels(&f)})
	}
	if queryBoolDefault(q.Get("forecast_tile"), true) && len(window) > 0 {
		f := render.ForecastTileFrame(window)
		p.Frames = append(p.Frames, render.CardFrame{Card: "forecast", Pixels: render.HexPixels(&f)})
	}
	if queryBoolDefault(q.Get("air_tile"), true) {
		air, haveAir := a.weather.currentAir()
		if !haveAir {
			air = sampleAirObservation(now)
		}
		f := render.AirTileFrame(air.AQI, air.HourlyAQI)
		p.Frames = append(p.Frames, render.CardFrame{Card: "air", Pixels: render.HexPixels(&f)})
	}
	writeJSON(w, http.StatusOK, p)
}

// sampleAirObservation backs the air preview before the first real fetch: a
// moderate reading easing off overnight, so the strip shows bucket variety.
func sampleAirObservation(now time.Time) airObservation {
	hourly := make([]float64, 24)
	for i := range hourly {
		hourly[i] = 42 - 24*math.Sin(float64(i)/24*math.Pi)
	}
	return airObservation{AQI: 42, PM25: 12, PM10: 19, HourlyAQI: hourly, FetchedAt: now}
}

// sampleWeatherObservation backs the preview when no real fetch has happened:
// a mild partly-cloudy day with a plausible sinusoidal 24h temperature arc.
func sampleWeatherObservation(now time.Time) weatherObservation {
	hourly := make([]float64, 24)
	for i := range hourly {
		hourly[i] = 16 + 6*math.Sin((float64(i)-3)/24*2*math.Pi)
	}
	return weatherObservation{Condition: render.WeatherClouds, TempC: 21, Hourly: hourly, FetchedAt: now}
}
