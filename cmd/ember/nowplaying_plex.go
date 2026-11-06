package main

import (
	"crypto/subtle"
	"errors"
	"net/http"
)

func (a *App) handlePlexWebhook(w http.ResponseWriter, r *http.Request) {
	plex := a.nowPlaying.Plex
	if plex == nil || plex.Cfg.WebhookKey == "" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(plex.Cfg.WebhookKey)) != 1 {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	plex.Wake()
	w.WriteHeader(http.StatusNoContent)
}
