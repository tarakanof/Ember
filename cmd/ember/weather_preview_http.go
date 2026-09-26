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

// handleWeatherPreview renders the weather tiles under a draft config into
// the same 32×8 frame grids as /v1/preview. Open and read-only. The frames
// come from the same tile views the coordinator pushes (previewTiles), so they
// match the clock by construction. Uses the live observations when they exist,
// else canned samples so the preview never renders blank (before the first
// fetch, or with the widget disabled).
//
// Query params (the draft; everything else comes from the live config):
//   - rotate_in_apps   bool (default true)  → "weather" frame
//   - forecast_tile    bool (default true)  → "forecast" frame
//   - air_tile         bool (default true)  → "air" frame
//   - forecast_hours   int (default 24; <=0 or >24 means 24, as on the device)
//   - units            "metric"|"imperial" (default "metric")
//   - moon_phase       bool (default: the saved config's)
//   - lat, lon         float pair (default: the saved location). Used only
//     when both parse and are in range; otherwise both come from the saved
//     config, so a half-typed coordinate can't move the moon.
//
// The moon phase and location are draft params because the Settings pane
// previews a change before its autosave lands; reading only the saved config
// would render the old moon and never refresh. Payload-only, because the canvas can't animate them: with
// tile_native_icons the gallery icon (the preview draws the condition sprite
// at cols 0-7), and the NG precipitation overlay.
func (a *App) handleWeatherPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherPreview(r.URL.Query(), time.Now()))
}

func (a *App) weatherPreview(q url.Values, now time.Time) render.Preview {
	cfg := a.cfg.Load().Weather
	cfg.RotateInApps = boolPtr(queryBoolDefault(q.Get("rotate_in_apps"), true))
	cfg.ForecastTile = boolPtr(queryBoolDefault(q.Get("forecast_tile"), true))
	cfg.AirTile = boolPtr(queryBoolDefault(q.Get("air_tile"), true))
	cfg.ForecastHours = 24
	if v, err := strconv.Atoi(q.Get("forecast_hours")); err == nil {
		cfg.ForecastHours = v
	}
	cfg.Units = "metric"
	if strings.TrimSpace(q.Get("units")) == "imperial" {
		cfg.Units = "imperial"
	}
	cfg.MoonPhase = boolPtr(queryBoolDefault(q.Get("moon_phase"), cfg.MoonPhaseEnabled()))
	if lat, lon, ok := queryLatLon(q); ok {
		cfg.Latitude, cfg.Longitude = lat, lon
	}

	in := tileInputs{now: now, weather: cfg}
	var have bool
	if in.obs, have = a.weather.current(); !have {
		in.obs = sampleWeatherObservation(now)
	}
	if in.air, have = a.weather.currentAir(); !have {
		in.air = sampleAirObservation(now)
	}
	return previewTiles(in, weatherTile.card, forecastTile.card, airTile.card)
}

// queryLatLon reads the lat/lon draft pair: ok only when both are finite
// numbers within the ranges WeatherConfig.validate accepts.
func queryLatLon(q url.Values) (lat, lon float64, ok bool) {
	lat, errLat := strconv.ParseFloat(strings.TrimSpace(q.Get("lat")), 64)
	lon, errLon := strconv.ParseFloat(strings.TrimSpace(q.Get("lon")), 64)
	if errLat != nil || errLon != nil || math.IsNaN(lat) || math.IsNaN(lon) ||
		lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
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
