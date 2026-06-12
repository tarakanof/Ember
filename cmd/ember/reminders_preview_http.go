package main

import (
	"net/http"
	"strings"

	"github.com/tarakanof/ember/internal/render"
)

// handleReminderPreview renders the drawn reminder-alarm popup (gold bell +
// text) in the same 32×8 grid as /v1/preview. Open and read-only. The device
// scrolls the full text and may swap the bell for a native icon; the preview
// always shows the drawn bell with whatever text fits.
//
// Query params:
//   - text  sample reminder title (default "Stand up")
func (a *App) handleReminderPreview(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if text == "" {
		text = "Stand up"
	}
	if rs := []rune(text); len(rs) > 32 {
		text = string(rs[:32]) // anything past the canvas is clipped anyway
	}
	f := render.ReminderPopupFrame(text)
	writeJSON(w, http.StatusOK, render.Preview{
		Width: 32, Height: 8,
		Frames: []render.CardFrame{{Card: "reminder", Pixels: render.HexPixels(&f)}},
	})
}
