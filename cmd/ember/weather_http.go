package main

import (
	"net/http"
)

type weatherConfigDTO struct {
	WeatherConfig
	ProvidersAvailable []string `json:"providers_available"`
	ProviderHeld       string   `json:"provider_held,omitempty"`
}

func (a *App) weatherDTO(c WeatherConfig) weatherConfigDTO {
	d := weatherConfigDTO{WeatherConfig: c, ProvidersAvailable: a.weatherFetcher.providersAvailable()}
	if p := a.weather.heldProvider.Load(); p != nil {
		d.ProviderHeld = *p
	}
	return d
}

func (a *App) handleWeatherConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.weatherDTO(a.settings.weather.get()))
}

func (a *App) handleWeatherConfigPut(w http.ResponseWriter, r *http.Request) {
	held := a.weather.heldProvider.Swap(nil)
	c, ok := serveSettingPut(a, w, r, a.settings.weather)
	if !ok {
		a.weather.heldProvider.CompareAndSwap(nil, held)
		return
	}
	writeJSON(w, http.StatusOK, a.weatherDTO(c))
}
