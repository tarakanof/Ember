package main

import (
	"fmt"
	"net/http"
)

const displaySettingsKey = "display_json"

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
