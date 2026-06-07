package main

import (
	"net/http"
)

// initReminders opens the shared store and applies any reminders persisted by a
// previous run. The eval loop is started separately (StartReminders).
func (a *App) initReminders(cfg Config) error {
	if err := a.ensureStore(cfg.Pomodoro.DBPath); err != nil {
		return err
	}
	a.loadPersistedReminderSettings()
	return nil
}

func (a *App) handleRemindersConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cfg.Load().Reminders)
}

func (a *App) handleRemindersConfigPut(w http.ResponseWriter, r *http.Request) {
	var cfg RemindersConfig
	if err := decodeJSON(w, r, &cfg, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.applyReminderSettings(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, a.cfg.Load().Reminders)
}
