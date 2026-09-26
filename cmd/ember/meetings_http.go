package main

import (
	"net/http"
)

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
	if _, ok := serveSettingPut(a, w, r, a.settings.meetings); ok {
		writeJSON(w, http.StatusOK, a.meetingsDTO())
	}
}
