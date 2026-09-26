package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// weatherTileStaleTTL clears the weather tile if no fresh observation arrived
// within this window (≈3× the default 10-min poll), so a wedged poller doesn't
// leave a stale temperature on the device indefinitely.
const weatherTileStaleTTL = 30 * time.Minute

// reconcileWeatherApp pushes/refreshes the single "ember-weather" rotating tile
// when the feature is enabled, set to rotate, and has a fresh observation; it
// clears the tile otherwise. Coordinator goroutine only.
func (c *coordinator) reconcileWeatherApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	want := cfg.Enabled && cfg.RotateInAppsEnabled() && have && now.Sub(obs.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-weather", &c.pushedWeather, want, func() map[string]any {
		tempText := weatherTempText(obs.TempC, cfg.Units)
		window := forecastWindow(obs.Hourly, cfg.ForecastHours)
		var p map[string]any
		switch moon := weatherTileMoon(cfg, obs, now); {
		case moon != nil:
			// Moon wins over native icons — there is no per-phase gallery set.
			p = render.WeatherPayloadMoon(tempText, obs.TempC, window, *moon, usageAppLifetime)
		case cfg.TileNativeIcons:
			p = render.WeatherPayloadNative(cfg.weatherIconID(obs.Condition), tempText, obs.TempC, window, usageAppLifetime)
		default:
			p = render.WeatherPayload(obs.Condition, tempText, obs.TempC, window, usageAppLifetime)
		}
		return render.WithOverlay(p, weatherOverlay(obs, cfg))
	})
}

// weatherTileMoon is the moon phase the conditions tile shows instead of its
// icon on a clear night (moon_phase on, location set), or nil.
func weatherTileMoon(cfg WeatherConfig, obs weatherObservation, now time.Time) *render.MoonView {
	if !cfg.MoonPhaseEnabled() || obs.Condition != render.WeatherClear ||
		(cfg.Latitude == 0 && cfg.Longitude == 0) || !isNight(cfg.Latitude, cfg.Longitude, now) {
		return nil
	}
	illum, waxing := moonIllumination(now)
	return &render.MoonView{Illum: illum, Waxing: waxing}
}

// forecastWindow returns the first `hours` hourly temps (hours clamped to a sane
// 1..24), or the whole slice when shorter. nil/empty in → nil out.
func forecastWindow(hourly []float64, hours int) []float64 {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24 {
		hours = 24
	}
	if len(hourly) > hours {
		return hourly[:hours]
	}
	return hourly
}

// reconcileForecastApp pushes/refreshes the standalone "ember-forecast" tile
// (hourly temperature bars) when weather is enabled, the forecast tile is turned
// on, and we have fresh hourly data; clears it otherwise. Coordinator goroutine only.
func (c *coordinator) reconcileForecastApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	obs, have := c.weather.current()
	hourly := forecastWindow(obs.Hourly, cfg.ForecastHours)
	want := cfg.Enabled && cfg.ForecastTileEnabled() && have && len(hourly) > 0 &&
		now.Sub(obs.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-forecast", &c.pushedForecast, want, func() map[string]any {
		return render.ForecastPayload(hourly, usageAppLifetime)
	})
}

// reconcileAirApp pushes/refreshes the standalone "ember-air" tile (current
// European AQI + hourly trend strip) when weather is enabled, the air tile is
// turned on, and the air observation is fresh; clears it otherwise. Coordinator
// goroutine only.
func (c *coordinator) reconcileAirApp(now time.Time) {
	if c.weather == nil {
		return
	}
	cfg := c.loadCfg().Weather
	air, have := c.weather.currentAir()
	want := cfg.Enabled && cfg.AirTileEnabled() && have && now.Sub(air.FetchedAt) < weatherTileStaleTTL
	c.reconcileTile(now, "ember-air", &c.pushedAir, want, func() map[string]any {
		return render.AirPayload(air.AQI, air.HourlyAQI, usageAppLifetime)
	})
}
