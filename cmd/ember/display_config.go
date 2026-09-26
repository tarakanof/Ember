package main

import (
	"fmt"
	"net/http"
)

const displaySettingsKey = "display_json"

// displayConfigDTO is the runtime-editable slice of DisplayConfig
// (GET/PUT /v1/display/config). Values are EFFECTIVE: config.json is the
// baseline, the store override (this DTO) wins after first PUT; a PUT merges
// (omitted fields keep their value, see settings_overlay.go).
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

// displaySettingSpec registers the display knobs with the settings overlay.
// IdleRestoreSeconds is integer-divided by 60; a file baseline like 90s rounds
// down to 1 minute, acceptable since the DTO only allows whole minutes. The
// coordinator reads these fields live, so a change takes effect on the next
// reconcile tick.
func displaySettingSpec() settingSpec[displayConfigDTO] {
	return settingSpec[displayConfigDTO]{
		key: displaySettingsKey,
		view: func(c Config) displayConfigDTO {
			return displayConfigDTO{
				IdleHideMinutes:      c.Display.IdleRestoreSeconds / 60,
				AttentionHoldSeconds: c.Display.AckTimeoutSeconds,
				AttentionChime:       c.Display.AttentionChime,
			}
		},
		apply: func(c *Config, d displayConfigDTO) error {
			if err := d.validate(); err != nil {
				return err
			}
			c.Display.IdleRestoreSeconds = d.IdleHideMinutes * 60
			c.Display.AckTimeoutSeconds = d.AttentionHoldSeconds
			c.Display.AttentionChime = d.AttentionChime
			return nil
		},
	}
}

func (a *App) handleDisplayConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.display)
}

func (a *App) handleDisplayConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.display); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
