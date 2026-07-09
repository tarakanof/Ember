package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const displaySettingsKey = "display_json"

// displayConfigDTO is the runtime-editable slice of DisplayConfig
// (GET/PUT /v1/display/config). Values are EFFECTIVE: config.json is the
// baseline, the store override (this DTO) wins after first PUT.
type displayConfigDTO struct {
	IdleHideMinutes      int  `json:"idle_hide_minutes"`
	AttentionHoldSeconds int  `json:"attention_hold_seconds"`
	AttentionChime       bool `json:"attention_chime"`
}

func (d displayConfigDTO) validate() error {
	if d.IdleHideMinutes < 0 || d.IdleHideMinutes > 60 {
		return fmt.Errorf("idle_hide_minutes %d out of range [0, 60]", d.IdleHideMinutes)
	}
	if d.AttentionHoldSeconds < 5 || d.AttentionHoldSeconds > 300 {
		return fmt.Errorf("attention_hold_seconds %d out of range [5, 300]", d.AttentionHoldSeconds)
	}
	return nil
}

// displayDTO converts the live config into the runtime DTO.
// IdleRestoreSeconds is integer-divided by 60; a file-baseline like 90s
// rounds down to 1 minute — acceptable since the DTO only allows whole minutes.
func (a *App) displayDTO() displayConfigDTO {
	c := a.cfg.Load()
	return displayConfigDTO{
		IdleHideMinutes:      c.Display.IdleRestoreSeconds / 60,
		AttentionHoldSeconds: c.Display.AckTimeoutSeconds,
		AttentionChime:       c.Display.AttentionChime,
	}
}

// applyDisplaySettings swaps the display knobs into the live config and
// persists them to the store so they survive restarts. The coordinator reads
// these fields live, so the change takes effect on the next reconcile tick.
func (a *App) applyDisplaySettings(dto displayConfigDTO) {
	a.updateConfig(func(cur *Config) {
		cur.Display.IdleRestoreSeconds = dto.IdleHideMinutes * 60
		cur.Display.AckTimeoutSeconds = dto.AttentionHoldSeconds
		cur.Display.AttentionChime = dto.AttentionChime
	})
	if a.store != nil {
		if blob, err := json.Marshal(dto); err == nil {
			if err := a.store.PutSetting(displaySettingsKey, string(blob)); err != nil {
				a.logger.Warn("display settings persist failed", "err", err)
			}
		}
	}
}

// loadPersistedDisplaySettings re-applies any previously PUT display config
// from the store over the current (file-baseline) config. Called at startup and
// after POST /admin/reload to prevent a reload from clobbering the store override.
func (a *App) loadPersistedDisplaySettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(displaySettingsKey)
	if err != nil || !ok {
		return
	}
	var dto displayConfigDTO
	if err := json.Unmarshal([]byte(blob), &dto); err != nil {
		a.logger.Warn("display persisted settings parse failed", "err", err)
		return
	}
	if err := dto.validate(); err != nil {
		a.logger.Warn("display persisted settings invalid; ignoring", "err", err)
		return
	}
	a.applyDisplaySettings(dto)
}

func (a *App) handleDisplayConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.displayDTO())
}

func (a *App) handleDisplayConfigPut(w http.ResponseWriter, r *http.Request) {
	var dto displayConfigDTO
	if err := decodeJSON(w, r, &dto, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := dto.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.applyDisplaySettings(dto)
	writeJSON(w, http.StatusOK, a.displayDTO())
}
