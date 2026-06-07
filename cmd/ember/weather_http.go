package main

import (
	"net/http"
)

// initWeather opens the shared store (reusing the Pomodoro DB path) and applies
// any weather settings persisted by a previous run, so menu edits survive
// restarts even when Pomodoro is disabled. The poll loop is started separately
// (StartWeather) and is a no-op while the feature is disabled.
func (a *App) initWeather(cfg Config) error {
	if err := a.ensureStore(cfg.Pomodoro.DBPath); err != nil {
		return err
	}
	a.loadPersistedWeatherSettings()
	a.loadHiddenApps() // ensure the hidden-app set loads even if Pomodoro is off
	return nil
}

func (a *App) handleWeatherConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cfg.Load().Weather)
}

func (a *App) handleWeatherConfigPut(w http.ResponseWriter, r *http.Request) {
	var cfg WeatherConfig
	if err := decodeJSON(w, r, &cfg, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.applyWeatherSettings(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, a.cfg.Load().Weather)
}
