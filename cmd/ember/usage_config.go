package main

import (
	"encoding/json"
	"net/http"
)

// usageSettingsKey is the writable-store key for the runtime usage-widget
// toggles (overrides the config.json baseline, mirrors weather/pomodoro).
const usageSettingsKey = "usage_json"

type usageConfigDTO struct {
	UsageWidget   bool `json:"usage_widget"`
	UsagePerModel bool `json:"usage_per_model"`
}

func (a *App) usageDTO() usageConfigDTO {
	c := a.cfg.Load()
	return usageConfigDTO{UsageWidget: c.usageWidgetEnabled(), UsagePerModel: c.usagePerModelEnabled()}
}

// applyUsageSettings swaps the toggles into the live config and persists them.
// The coordinator reads usageWidgetEnabled()/usagePerModelEnabled() live, so the
// change takes effect on the next reconcile tick.
func (a *App) applyUsageSettings(dto usageConfigDTO) {
	cur := *a.cfg.Load()
	uw, upm := dto.UsageWidget, dto.UsagePerModel
	cur.UsageWidget = &uw
	cur.UsagePerModel = &upm
	a.cfg.Store(&cur)
	if a.store != nil {
		if blob, err := json.Marshal(dto); err == nil {
			if err := a.store.PutSetting(usageSettingsKey, string(blob)); err != nil {
				a.logger.Warn("usage settings persist failed", "err", err)
			}
		}
	}
	a.nudgePomo() // prompt a prompt re-render so the toggle is reflected quickly
}

func (a *App) loadPersistedUsageSettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(usageSettingsKey)
	if err != nil || !ok {
		return
	}
	var dto usageConfigDTO
	if err := json.Unmarshal([]byte(blob), &dto); err != nil {
		a.logger.Warn("usage persisted settings parse failed", "err", err)
		return
	}
	a.applyUsageSettings(dto)
}

func (a *App) handleUsageConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.usageDTO())
}

func (a *App) handleUsageConfigPut(w http.ResponseWriter, r *http.Request) {
	var dto usageConfigDTO
	if err := decodeJSON(w, r, &dto, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.applyUsageSettings(dto)
	writeJSON(w, http.StatusOK, a.usageDTO())
}
