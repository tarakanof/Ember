package main

import (
	"net/http"
)

func (a *App) handleWeatherConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.weather)
}

func (a *App) handleWeatherConfigPut(w http.ResponseWriter, r *http.Request) {
	if c, ok := serveSettingPut(a, w, r, a.settings.weather); ok {
		writeJSON(w, http.StatusOK, c)
	}
}
