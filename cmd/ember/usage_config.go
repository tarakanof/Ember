package main

import (
	"encoding/json"
	"net/http"
)

// usageSettingsKey is the writable-store key for the runtime usage-widget
// toggles (overrides the config.json baseline, mirrors weather/pomodoro).
const usageSettingsKey = "usage_json"

type usageConfigDTO struct {
	UsageWidget       bool `json:"usage_widget"`
	UsagePerModel     bool `json:"usage_per_model"`
	LimitAlarm        bool `json:"limit_alarm"`
	UsageThresholdPct int  `json:"usage_threshold_pct"`
}

func (a *App) usageDTO() usageConfigDTO {
	c := a.cfg.Load()
	return usageConfigDTO{
		UsageWidget:       c.usageWidgetEnabled(),
		UsagePerModel:     c.usagePerModelEnabled(),
		LimitAlarm:        c.limitAlarmEnabled(),
		UsageThresholdPct: c.usageThresholdPct(),
	}
}

// applyUsageSettings swaps the toggles into the live config and persists them.
// The coordinator reads usageWidgetEnabled()/usagePerModelEnabled()/limitAlarmEnabled()
// live, so the change takes effect on the next reconcile tick.
func (a *App) applyUsageSettings(dto usageConfigDTO) {
	cur := *a.cfg.Load()
	uw, upm, la := dto.UsageWidget, dto.UsagePerModel, dto.LimitAlarm
	cur.UsageWidget = &uw
	cur.UsagePerModel = &upm
	cur.LimitAlarm = &la
	thr := dto.UsageThresholdPct
	if thr < 0 {
		thr = 0
	}
	if thr > 100 {
		thr = 100
	}
	cur.UsageThresholdPct = &thr
	a.cfg.Store(&cur)
	dto.UsageThresholdPct = thr // persist the clamped value, not the raw PUT value
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
	// Pre-seed from the current config so missing keys in a legacy blob (e.g.
	// blobs written before limit_alarm existed) keep their default values
	// rather than unmarshalling as false and silently disabling the feature.
	dto := a.usageDTO()
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
	// Pre-seed from the live config so a partial body only changes the fields
	// it names (same schema-evolution guard as loadPersistedUsageSettings) —
	// otherwise a future client omitting a newer field would silently zero it.
	dto := a.usageDTO()
	if err := decodeJSON(w, r, &dto, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.applyUsageSettings(dto)
	writeJSON(w, http.StatusOK, a.usageDTO())
}
