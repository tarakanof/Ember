package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// MeetingsConfig holds the next-meeting widget's runtime-editable settings.
// The ICS feed URLs are NOT here: they are credentials (possession = calendar
// read access) and live only in the EMBER_MEETINGS_ICS_URLS env var, never in
// JSON, the store, logs, or API responses (the config GET reports a count).
type MeetingsConfig struct {
	Enabled          bool `json:"enabled"`
	TileLeadMinutes  int  `json:"tile_lead_minutes"`  // tile joins rotation within this window
	PopupLeadMinutes int  `json:"popup_lead_minutes"` // 0 = no popup
	Chime            bool `json:"chime"`
}

func (c *MeetingsConfig) applyDefaults() {
	// Friendly defaults applied only at config LOAD (this runs over the file
	// config). The menu's explicit values are layered on top afterwards by
	// loadPersistedMeetingsSettings, whose applyMeetingsSettings does NOT
	// re-run applyDefaults — so a user can still turn these off (set
	// popup_lead=0, enabled=false) and have it stick. Mirrors the Weather
	// and Pomodoro convention.
	if !c.Enabled {
		c.Enabled = true
	}
	if c.TileLeadMinutes <= 0 {
		c.TileLeadMinutes = 60
	} else if c.TileLeadMinutes > 480 {
		c.TileLeadMinutes = 480
	}
	if c.PopupLeadMinutes < 0 {
		c.PopupLeadMinutes = 0
	} else if c.PopupLeadMinutes == 0 {
		c.PopupLeadMinutes = 2
	}
	if !c.Chime {
		c.Chime = true
	}
}

func validateMeetings(c MeetingsConfig) error {
	if c.TileLeadMinutes < 1 || c.TileLeadMinutes > 480 {
		return errors.New("meetings.tile_lead_minutes must be 1..480")
	}
	if c.PopupLeadMinutes < 0 || c.PopupLeadMinutes > 60 {
		return errors.New("meetings.popup_lead_minutes must be 0..60")
	}
	return nil
}

// ---- config persistence (mirrors the Weather store pattern) ----

const meetingsSettingsKey = "meetings_json"

func (a *App) applyMeetingsSettings(cfg MeetingsConfig) error {
	// NB: do NOT call cfg.applyDefaults() here. This is the runtime write/persist
	// path (menu PUT + loadPersistedMeetingsSettings); re-defaulting would force
	// the opt-in toggles back on and rewrite popup_lead=0 → 2, making 0 (no
	// popup) impossible to keep. The menu always sends a complete config, so we
	// validate verbatim (mirrors applyWeatherSettings / applyPomodoroSettings).
	if err := validateMeetings(cfg); err != nil {
		return err
	}
	cur := *a.cfg.Load()
	cur.Meetings = cfg
	a.cfg.Store(&cur)
	if a.store != nil {
		if blob, err := json.Marshal(cfg); err == nil {
			if err := a.store.PutSetting(meetingsSettingsKey, string(blob)); err != nil {
				a.logger.Warn("meetings settings persist failed", "err", err)
			}
		}
	}
	a.nudgePomo()
	return nil
}

func (a *App) loadPersistedMeetingsSettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(meetingsSettingsKey)
	if err != nil || !ok {
		return
	}
	var cfg MeetingsConfig
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		a.logger.Warn("meetings persisted settings parse failed", "err", err)
		return
	}
	if err := a.applyMeetingsSettings(cfg); err != nil {
		a.logger.Warn("meetings persisted settings invalid, ignoring", "err", err)
	}
}

// parseICSURLs splits a comma-separated list of ICS feed URLs, trims whitespace,
// and keeps only entries with an http:// or https:// scheme. Returns nil for
// empty input or no valid entries.
func parseICSURLs(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			out = append(out, p)
		}
	}
	return out
}
