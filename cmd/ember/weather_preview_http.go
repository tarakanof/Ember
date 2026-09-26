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

// handleWeatherPreview renders the weather/forecast tiles under a draft config
// into the same 32×8 frame grids as /v1/preview. Open and read-only. Uses the
// live observation when one exists, else a canned sample so the preview never
// renders blank (before the first fetch, or with the widget disabled).
//
// Query params:
//   - rotate_in_apps   bool (default true)  → "weather" frame
//   - forecast_tile    bool (default true)  → "forecast" frame
//   - air_tile         bool (default true)  → "air" frame
//   - forecast_hours   int (default 24; <=0 or >24 means 24, as on the device)
//   - units            "metric"|"imperial" (default "metric")
//
// tile_native_icons is deliberately not a param: the canvas can't animate
// gallery icons, so the preview always shows the drawn sprite. The moon phase
// follows the live config (moon_phase, location) exactly as on the device; the
// NG precipitation overlay is animated by the firmware and is not drawn.
func (a *App) handleWeatherPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherPreview(r.URL.Query(), time.Now()))
}

func (a *App) weatherPreview(q url.Values, now time.Time) render.Preview {
	obs, have := a.weather.current()
	if !have {
		obs = sampleWeatherObservation(now)
	}
	units := strings.TrimSpace(q.Get("units"))
	if units != "imperial" {
		units = "metric"
	}
	// forecastWindow applies the device's rule (<=0 or >24 → 24), so a stored
	// value the PUT path let through previews exactly as the clock shows it.
	hours := 24
	if v, err := strconv.Atoi(q.Get("forecast_hours")); err == nil {
		hours = v
	}
	tempText := weatherTempText(obs.TempC, units)
	window := forecastWindow(obs.Hourly, hours)

	p := render.Preview{Width: 32, Height: 8, Frames: []render.CardFrame{}}
	if queryBoolDefault(q.Get("rotate_in_apps"), true) {
		cfg := a.cfg.Load().Weather
		f := render.WeatherTileFrame(obs.Condition, tempText, obs.TempC, window, weatherTileMoon(cfg, obs, now))
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
	return p
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
