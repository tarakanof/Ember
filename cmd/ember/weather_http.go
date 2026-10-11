package main

import (
	"net/http"
)

type weatherConfigDTO struct {
	WeatherConfig
	ProvidersAvailable []string `json:"providers_available"`
}

func (a *App) weatherDTO(c WeatherConfig) weatherConfigDTO {
	return weatherConfigDTO{WeatherConfig: c, ProvidersAvailable: a.weatherFetcher.providersAvailable()}
}

func (a *App) handleWeatherConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherDTO(a.settings.weather.get()))
}

func (a *App) handleWeatherConfigPut(w http.ResponseWriter, r *http.Request) {
	if c, ok := serveSettingPut(a, w, r, a.settings.weather); ok {
		writeJSON(w, http.StatusOK, a.weatherDTO(c))
	}
}
