package main

import (
	_ "embed"
	"net/http"
)

//go:embed pomodoro_dashboard.html
var dashboardHTML string

func (a *App) handlePomodoroDashboard(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusNotFound, errPomodoroDisabled)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(dashboardHTML))
}
