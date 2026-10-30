package main

import (
	"net/http"

	"github.com/tarakanof/ember/internal/weather"
)

type weatherConfigDTO struct {
	weather.Config
	ProvidersAvailable []string `json:"providers_available"`
}

func (a *App) weatherDTO(c weather.Config) weatherConfigDTO {
	return weatherConfigDTO{Config: c, ProvidersAvailable: a.weatherFetcher.ProvidersAvailable()}
}

func (a *App) handleWeatherConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherDTO(a.settings.weather.get()))
}

func (a *App) handleWeatherConfigPut(w http.ResponseWriter, r *http.Request) {
	if c, ok := serveSettingPut(a, w, r, a.settings.weather); ok {
		writeJSON(w, http.StatusOK, a.weatherDTO(c))
	}
}
