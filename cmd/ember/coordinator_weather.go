package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

var weatherTile = tile{
	app:    "ember-weather",
	card:   "weather",
	toggle: func(in *tileInputs) bool { return in.weather.RotateInAppsEnabled() },
	live:   func(in *tileInputs) bool { return in.wx.live() },
	view: func(in *tileInputs) (tileView, bool) {
		cfg, obs := in.weather, in.wx.Obs
		tempText := weatherTempText(obs.TempC, cfg.Units)
		window := forecastWindow(obs.Hourly, cfg.ForecastHours)
		moon := weatherTileMoon(cfg, in.wx, in.now)
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
	live:   func(in *tileInputs) bool { return in.wx.live() },
	view: func(in *tileInputs) (tileView, bool) {
		hourly := forecastWindow(in.wx.Obs.Hourly, in.weather.ForecastHours)
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
	live:   func(in *tileInputs) bool { return in.wx.airLive() },
	view: func(in *tileInputs) (tileView, bool) {
		aqi, hourly := in.wx.Air.AQI, in.wx.Air.HourlyAQI
		return tileView{
			payload: render.AirPayload(aqi, hourly, usageAppLifetime),
			frame:   func() render.Frame { return render.AirTileFrame(aqi, hourly) },
		}, true
	},
}

func weatherTileMoon(cfg WeatherConfig, wx weatherState, now time.Time) *render.MoonView {
	if !cfg.MoonPhaseEnabled() || wx.Obs.Condition != render.WeatherClear || !wx.HaveSun || !wx.Night {
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
