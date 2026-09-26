package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// weatherTileStaleTTL clears the weather tiles if no fresh observation arrived
// within this window (≈3× the default 10-min poll), so a wedged poller doesn't
// leave a stale temperature on the device indefinitely.
const weatherTileStaleTTL = 30 * time.Minute

// weatherLive is the device gate the weather tiles share: the feature is on
// and the reading fetched at at is recent enough (see weatherTileStaleTTL).
func weatherLive(in *tileInputs, have bool, at time.Time) bool {
	return in.weather.Enabled && have && in.now.Sub(at) < weatherTileStaleTTL
}

// weatherTile is "ember-weather": the condition icon (or moon phase), the
// temperature and the hourly strip. Frame and payload come from one resolution
// of units, forecast window and moon, so the preview can't drift from the
// clock. The native gallery icon and the NG overlay are payload-only: the
// preview frame draws the sprite at cols 0-7 and no overlay.
var weatherTile = tile{
	app:    "ember-weather",
	card:   "weather",
	toggle: func(in *tileInputs) bool { return in.weather.RotateInAppsEnabled() },
	live:   func(in *tileInputs) bool { return weatherLive(in, in.haveObs, in.obs.FetchedAt) },
	view: func(in *tileInputs) (tileView, bool) {
		cfg, obs := in.weather, in.obs
		tempText := weatherTempText(obs.TempC, cfg.Units)
		window := forecastWindow(obs.Hourly, cfg.ForecastHours)
		moon := weatherTileMoon(cfg, obs, in.now)
		var p map[string]any
		switch {
		case moon != nil:
			// Moon wins over native icons — there is no per-phase gallery set.
			p = render.WeatherPayloadMoon(tempText, obs.TempC, window, *moon, usageAppLifetime)
		case cfg.TileNativeIcons:
			p = render.WeatherPayloadNative(cfg.weatherIconID(obs.Condition), tempText, obs.TempC, window, usageAppLifetime)
		default:
			p = render.WeatherPayload(obs.Condition, tempText, obs.TempC, window, usageAppLifetime)
		}
		return tileView{
			payload: render.WithOverlay(p, weatherOverlay(obs, cfg)),
			frame:   render.WeatherTileFrame(obs.Condition, tempText, obs.TempC, window, moon),
		}, true
	},
}

// forecastTile is "ember-forecast": hourly temperature bars. Without hourly
// data it has nothing to show.
var forecastTile = tile{
	app:    "ember-forecast",
	card:   "forecast",
	toggle: func(in *tileInputs) bool { return in.weather.ForecastTileEnabled() },
	live:   func(in *tileInputs) bool { return weatherLive(in, in.haveObs, in.obs.FetchedAt) },
	view: func(in *tileInputs) (tileView, bool) {
		hourly := forecastWindow(in.obs.Hourly, in.weather.ForecastHours)
		if len(hourly) == 0 {
			return tileView{}, false
		}
		return tileView{
			payload: render.ForecastPayload(hourly, usageAppLifetime),
			frame:   render.ForecastTileFrame(hourly),
		}, true
	},
}

// airTile is "ember-air": the European AQI and its hourly trend strip.
var airTile = tile{
	app:    "ember-air",
	card:   "air",
	toggle: func(in *tileInputs) bool { return in.weather.AirTileEnabled() },
	live:   func(in *tileInputs) bool { return weatherLive(in, in.haveAir, in.air.FetchedAt) },
	view: func(in *tileInputs) (tileView, bool) {
		return tileView{
			payload: render.AirPayload(in.air.AQI, in.air.HourlyAQI, usageAppLifetime),
			frame:   render.AirTileFrame(in.air.AQI, in.air.HourlyAQI),
		}, true
	},
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

// forecastWindow returns the first `hours` hourly temps (hours <= 0 or > 24
// means 24), or the whole slice when shorter. nil/empty in → nil out. The one
// forecast-hours rule for the device and the previews.
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
