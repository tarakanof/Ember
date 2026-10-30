package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	remindersSettingsKey   = "reminders_json"
	maxReminderLeadMinutes = 30
	minReminderPopupSec    = 1
	maxReminderPopupSec    = 300
	maxReminderIconID      = 64
)

type RemindersConfig struct {
	Enabled       bool   `json:"enabled"`
	Sound         bool   `json:"sound"`
	LeadMinutes   int    `json:"lead_minutes"`
	PopupDuration int    `json:"popup_duration"`
	UseNativeIcon bool   `json:"use_native_icon"`
	NativeIconID  string `json:"native_icon_id"`
	Hold          bool   `json:"hold"`
	RepeatSound   bool   `json:"repeat_sound"`
}

func defaultRemindersConfig() RemindersConfig {
	return RemindersConfig{Sound: true, PopupDuration: 8, Hold: true}
}

func (c RemindersConfig) validate() error {
	if c.LeadMinutes < 0 || c.LeadMinutes > maxReminderLeadMinutes {
		return fmt.Errorf("lead_minutes must be 0..%d", maxReminderLeadMinutes)
	}
	if c.PopupDuration < minReminderPopupSec || c.PopupDuration > maxReminderPopupSec {
		return fmt.Errorf("popup_duration must be %d..%d", minReminderPopupSec, maxReminderPopupSec)
	}
	if !utf8.ValidString(c.NativeIconID) || utf8.RuneCountInString(c.NativeIconID) > maxReminderIconID {
		return fmt.Errorf("native_icon_id must be at most %d characters", maxReminderIconID)
	}
	return nil
}

func remindersSettingSpec() settingSpec[RemindersConfig] {
	return settingSpec[RemindersConfig]{
		key:  remindersSettingsKey,
		view: func(c Config) RemindersConfig { return c.Reminders },
		apply: func(c *Config, d RemindersConfig) error {
			d.NativeIconID = strings.TrimSpace(d.NativeIconID)
			if err := d.validate(); err != nil {
				return err
			}
			c.Reminders = d
			return nil
		},
	}
}

func (a *App) handleRemindersConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.reminders)
}

var errRemindersNotDefault = errors.New("reminder settings are no longer the defaults")

func (a *App) handleRemindersConfigPut(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("if_default") != "1" {
		if d, ok := serveSettingPut(a, w, r, a.settings.reminders); ok {
			writeJSON(w, http.StatusOK, d)
		}
		return
	}
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		return
	}
	d, err := a.settings.reminders.putIf(patch, func(cur RemindersConfig) error {
		if cur != defaultRemindersConfig() {
			return errRemindersNotDefault
		}
		return nil
	})
	if errors.Is(err, errRemindersNotDefault) {
		a.logger.InfoContext(r.Context(), "reminders config conditional put refused", "reason", "not_default")
		writeJSON(w, http.StatusConflict, a.settings.reminders.get())
		return
	}
	if a.answerSettingPut(w, r, err) {
		writeJSON(w, http.StatusOK, d)
	}
}
