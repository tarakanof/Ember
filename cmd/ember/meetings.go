package main

import (
	"errors"
	"strings"
)

// MeetingsConfig holds the next-meeting widget's runtime-editable settings.
// The ICS feed URLs are NOT here: they are credentials (possession = calendar
// read access) and live only in the EMBER_MEETINGS_ICS_URLS env var, never in
// JSON, the store, logs, or API responses (the config GET reports a count).
// Enabled, Chime, and PopupLeadMinutes (0 = documented "no popup") are
// pointers so JSON can distinguish "absent" (nil → default) from an explicit
// false/0 — the usage_widget / WeatherConfig convention. fillAbsent resolves
// nil to the concrete default at every entry point, so marshalled config never
// carries nulls; readers use the accessors below.
type MeetingsConfig struct {
	Enabled          *bool `json:"enabled"`
	TileLeadMinutes  int   `json:"tile_lead_minutes"`  // tile joins rotation within this window
	PopupLeadMinutes *int  `json:"popup_lead_minutes"` // 0 = no popup
	Chime            *bool `json:"chime"`
}

// defaultMeetingsPopupLeadMinutes is the T-minus popup lead used when
// popup_lead_minutes is absent; an explicit 0 means "no popup".
const defaultMeetingsPopupLeadMinutes = 2

// IsEnabled / ChimeEnabled: nil (absent from JSON) means the friendly default
// (on); an explicit false is respected.
func (c MeetingsConfig) IsEnabled() bool    { return c.Enabled == nil || *c.Enabled }
func (c MeetingsConfig) ChimeEnabled() bool { return c.Chime == nil || *c.Chime }

// PopupLeadMins resolves the popup lead: nil → default 2, explicit 0 → off,
// negatives clamp to 0 (validate rejects them on the PUT path anyway).
func (c MeetingsConfig) PopupLeadMins() int {
	if c.PopupLeadMinutes == nil {
		return defaultMeetingsPopupLeadMinutes
	}
	if v := *c.PopupLeadMinutes; v > 0 {
		return v
	}
	return 0
}

// fillAbsent resolves nil optional fields to their concrete defaults WITHOUT
// touching explicit values — false / popup_lead=0 always stick. Runs at file
// load (applyDefaults) and on the runtime write path (meetingsSettingSpec)
// so config snapshots marshal to concrete values, never nulls. Mirrors
// WeatherConfig.fillAbsent.
func (c *MeetingsConfig) fillAbsent() {
	if c.Enabled == nil {
		c.Enabled = boolPtr(true)
	}
	if c.Chime == nil {
		c.Chime = boolPtr(true)
	}
	if c.PopupLeadMinutes == nil {
		c.PopupLeadMinutes = intPtr(defaultMeetingsPopupLeadMinutes)
	}
}

func (c *MeetingsConfig) applyDefaults() {
	c.fillAbsent()
	// File-load leniency (validate rejects negatives on the PUT path).
	if *c.PopupLeadMinutes < 0 {
		c.PopupLeadMinutes = intPtr(0)
	}
	if c.TileLeadMinutes <= 0 {
		c.TileLeadMinutes = 60
	} else if c.TileLeadMinutes > 480 {
		c.TileLeadMinutes = 480
	}
}

func validateMeetings(c MeetingsConfig) error {
	if c.TileLeadMinutes < 1 || c.TileLeadMinutes > 480 {
		return errors.New("meetings.tile_lead_minutes must be 1..480")
	}
	if c.PopupLeadMinutes != nil && (*c.PopupLeadMinutes < 0 || *c.PopupLeadMinutes > 60) {
		return errors.New("meetings.popup_lead_minutes must be 0..60")
	}
	return nil
}

// ---- runtime settings (see settings_overlay.go) ----

const meetingsSettingsKey = "meetings_json"

// meetingsSettingSpec registers the meetings config with the settings overlay.
// As for weather, fillAbsent resolves only JSON nulls left after the merge;
// explicit false / popup_lead=0 always stick.
func (a *App) meetingsSettingSpec() settingSpec[MeetingsConfig] {
	return settingSpec[MeetingsConfig]{
		key:  meetingsSettingsKey,
		view: func(c Config) MeetingsConfig { return c.Meetings },
		apply: func(c *Config, m MeetingsConfig) error {
			m.fillAbsent()
			if err := validateMeetings(m); err != nil {
				return err
			}
			c.Meetings = m
			return nil
		},
		after: func(Config) { a.nudgePomo() },
	}
}

// parseICSURLs splits a comma-separated list of ICS feed URLs, trims
// whitespace, and keeps only entries with an http://, https://, webcal://, or
// webcals:// scheme (scheme comparison is case-insensitive). webcal:// and
// webcals:// are rewritten to https:// — they are plain HTTP subscription
// links used by iCloud and other calendar providers. Returns nil urls and 0
// dropped for empty input. dropped counts non-empty entries that were rejected
// due to an unsupported scheme. URLs containing literal commas are unsupported
// by the comma separator (real Google/Outlook/iCloud feed URLs contain none).
func parseICSURLs(s string) (urls []string, dropped int) {
	if s == "" {
		return nil, 0
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		switch {
		case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
			urls = append(urls, p)
		case strings.HasPrefix(lower, "webcals://"):
			urls = append(urls, "https://"+p[len("webcals://"):])
		case strings.HasPrefix(lower, "webcal://"):
			urls = append(urls, "https://"+p[len("webcal://"):])
		default:
			dropped++
		}
	}
	return urls, dropped
}
