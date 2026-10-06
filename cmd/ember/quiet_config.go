package main

import (
	"fmt"
	"net/http"
	"time"
)

type QuietHoursConfig struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

func parseHHMM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func quietActive(startMin, endMin int, t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	switch {
	case startMin == endMin:
		return false
	case startMin < endMin:
		return m >= startMin && m < endMin
	default:
		return m >= startMin || m < endMin
	}
}

func (c Config) quietHoursWindow() (enabled bool, startMin, endMin int) {
	start, ok := parseHHMM(c.QuietHours.Start)
	if !ok {
		start = 22 * 60
	}
	end, ok := parseHHMM(c.QuietHours.End)
	if !ok {
		end = 8 * 60
	}
	return c.QuietHours.Enabled, start, end
}

func validateQuietHours(q QuietHoursConfig) error {
	if q.Start != "" {
		if _, ok := parseHHMM(q.Start); !ok {
			return fmt.Errorf("%w: quiet_hours.start %q must be HH:MM", ErrConfigValidate, q.Start)
		}
	}
	if q.End != "" {
		if _, ok := parseHHMM(q.End); !ok {
			return fmt.Errorf("%w: quiet_hours.end %q must be HH:MM", ErrConfigValidate, q.End)
		}
	}
	return nil
}

const quietSettingsKey = "quiet_json"

type quietConfigDTO struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

func (d quietConfigDTO) validate() error {
	if _, ok := parseHHMM(d.Start); !ok {
		return fmt.Errorf("start %q must be HH:MM", d.Start)
	}
	if _, ok := parseHHMM(d.End); !ok {
		return fmt.Errorf("end %q must be HH:MM", d.End)
	}
	return nil
}

func quietSettingSpec() settingSpec[quietConfigDTO] {
	return settingSpec[quietConfigDTO]{
		key: quietSettingsKey,
		view: func(c Config) quietConfigDTO {
			q := c.QuietHours
			d := quietConfigDTO{Enabled: q.Enabled, Start: q.Start, End: q.End}
			if d.Start == "" {
				d.Start = "22:00"
			}
			if d.End == "" {
				d.End = "08:00"
			}
			return d
		},
		apply: func(c *Config, d quietConfigDTO) error {
			if err := d.validate(); err != nil {
				return err
			}
			c.QuietHours = QuietHoursConfig{Enabled: d.Enabled, Start: d.Start, End: d.End}
			return nil
		},
	}
}

func (a *App) handleQuietConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.quiet)
}

func (a *App) handleQuietConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.quiet); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
