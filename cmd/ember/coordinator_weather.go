package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const weatherTileStaleTTL = 30 * time.Minute

func weatherLive(in *tileInputs, have bool, at time.Time) bool {
	return in.weather.Enabled && have && in.now.Sub(at) < weatherTileStaleTTL
}

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
			p = render.WeatherPayloadMoon(tempText, obs.TempC, window, *moon, usageAppLifetime)
		case cfg.TileNativeIcons:
			p = render.WeatherPayloadNative(cfg.weatherIconID(obs.Condition), tempText, obs.TempC, window, usageAppLifetime)
		default:
			p = render.WeatherPayload(obs.Condition, tempText, obs.TempC, window, usageAppLifetime)
		}
		return tileView{
			payload: render.WithOverlay(p, weatherOverlay(obs, cfg)),
			frame:   func() render.Frame { return render.WeatherTileFrame(obs.Condition, tempText, obs.TempC, window, moon) },
		}, true
	},
}

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
			frame:   func() render.Frame { return render.ForecastTileFrame(hourly) },
		}, true
	},
}

var airTile = tile{
	app:    "ember-air",
	card:   "air",
	toggle: func(in *tileInputs) bool { return in.weather.AirTileEnabled() },
	live:   func(in *tileInputs) bool { return weatherLive(in, in.haveAir, in.air.FetchedAt) },
	view: func(in *tileInputs) (tileView, bool) {
		aqi, hourly := in.air.AQI, in.air.HourlyAQI
		return tileView{
			payload: render.AirPayload(aqi, hourly, usageAppLifetime),
			frame:   func() render.Frame { return render.AirTileFrame(aqi, hourly) },
		}, true
	},
}

func weatherTileMoon(cfg WeatherConfig, obs weatherObservation, now time.Time) *render.MoonView {
	if !cfg.MoonPhaseEnabled() || obs.Condition != render.WeatherClear ||
		(cfg.Latitude == 0 && cfg.Longitude == 0) || !isNight(cfg.Latitude, cfg.Longitude, now, obs) {
		return nil
	}
	illum, waxing := moonIllumination(now)
	return &render.MoonView{Illum: illum, Waxing: waxing}
}

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
