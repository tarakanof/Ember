package main

import (
	"net/http"
)

// initMeetings opens the shared store and re-applies persisted meeting
// settings (menu edits survive restarts). The poll loop is started separately.
func (a *App) initMeetings(cfg Config) error {
	if err := a.ensureStore(cfg.Pomodoro.DBPath); err != nil {
		return err
	}
	a.loadPersistedMeetingsSettings()
	return nil
}

type meetingsConfigDTO struct {
	MeetingsConfig
	// IcsUrlsConfigured tells the menu whether feeds exist server-side without
	// ever echoing them (they're credentials).
	IcsUrlsConfigured int `json:"ics_urls_configured"`
}

func (a *App) meetingsDTO() meetingsConfigDTO {
	return meetingsConfigDTO{
		MeetingsConfig:    a.cfg.Load().Meetings,
		IcsUrlsConfigured: len(a.meetingsURLs),
	}
}

func (a *App) handleMeetingsConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.meetingsDTO())
}

func (a *App) handleMeetingsConfigPut(w http.ResponseWriter, r *http.Request) {
	var cfg MeetingsConfig
	if err := decodeJSON(w, r, &cfg, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.applyMeetingsSettings(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, a.meetingsDTO())
}
