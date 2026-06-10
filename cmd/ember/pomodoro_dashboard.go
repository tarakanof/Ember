package main

import (
	_ "embed"
	"net/http"
)

// dashboardHTML is the self-contained stats dashboard page (vanilla JS + inline
// SVG/CSS, no build step, no external assets). It fetches the JSON stats
// endpoints client-side, so all computation stays on the server. Served openly
// like the other Pomodoro reads.
//
//go:embed pomodoro_dashboard.html
var dashboardHTML string

// handlePomodoroDashboard serves GET /v1/pomodoro/dashboard.
func (a *App) handlePomodoroDashboard(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(dashboardHTML))
}
