package main

import (
	"net/http"
	"strings"

	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handleReminderPreview(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if text == "" {
		text = "Stand up"
	}
	if rs := []rune(text); len(rs) > 32 {
		text = string(rs[:32])
	}
	f := render.ReminderPopupFrame(text)
	writeJSON(w, http.StatusOK, render.Preview{
		Width: 32, Height: 8,
		Frames: []render.CardFrame{{Card: "reminder", Pixels: render.HexPixels(&f)}},
	})
}
