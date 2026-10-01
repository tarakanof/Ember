package main

import (
	"errors"
	"strings"
)

// MeetingsConfig holds the next-meeting widget's runtime-editable settings.
type MeetingsConfig struct {
	Enabled          *bool `json:"enabled"`
	TileLeadMinutes  int   `json:"tile_lead_minutes"`
	PopupLeadMinutes *int  `json:"popup_lead_minutes"`
	Chime            *bool `json:"chime"`
}

const defaultMeetingsPopupLeadMinutes = 2

// IsEnabled / ChimeEnabled: nil (absent from JSON) means the friendly default (on); an explicit false is respected.
func (c MeetingsConfig) IsEnabled() bool    { return c.Enabled == nil || *c.Enabled }
func (c MeetingsConfig) ChimeEnabled() bool { return c.Chime == nil || *c.Chime }

// PopupLeadMins resolves the popup lead: nil → default 2, explicit 0 → off, negatives clamp to 0 (validate rejects them on the PUT path anyway).
func (c MeetingsConfig) PopupLeadMins() int {
	if c.PopupLeadMinutes == nil {
		return defaultMeetingsPopupLeadMinutes
	}
	if v := *c.PopupLeadMinutes; v > 0 {
		return v
	}
	return 0
}

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

const meetingsSettingsKey = "meetings_json"

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
