package main

import (
	"fmt"
	"net/http"
	"time"
)

// QuietHoursConfig is the global night-mute window. During the window the
// quietPublisher silences all device audio (Notify sound/rtttl keys stripped,
// melody endpoints no-op); visual output is unaffected. Zero value: disabled,
// with Start/End defaulting to 22:00–08:00 via quietHoursWindow.
type QuietHoursConfig struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

// parseHHMM parses "HH:MM" into minutes since midnight. ok is false for
// anything time.Parse("15:04") rejects.
func parseHHMM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// quietActive reports whether t's wall-clock time falls inside [start, end)
// minutes-since-midnight. start > end is the overnight window (quiet when
// t >= start || t < end); start == end is the empty window (never quiet).
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

// quietHoursWindow returns the enabled flag and the effective window in
// minutes since midnight, defaulting unset/invalid bounds to 22:00 / 08:00.
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

// validateQuietHours rejects malformed HH:MM bounds. Empty strings are valid —
// quietHoursWindow substitutes the 22:00/08:00 defaults.
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

// quietSettingsKey is the writable-store key for the runtime quiet-hours
// override (wins over the config.json baseline after first PUT).
const quietSettingsKey = "quiet_json"

type quietConfigDTO struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// validate rejects missing or malformed HH:MM bounds. Unlike validateQuietHours,
// the DTO requires explicit values — it never falls back to defaults.
func (d quietConfigDTO) validate() error {
	if _, ok := parseHHMM(d.Start); !ok {
		return fmt.Errorf("start %q must be HH:MM", d.Start)
	}
	if _, ok := parseHHMM(d.End); !ok {
		return fmt.Errorf("end %q must be HH:MM", d.End)
	}
	return nil
}

// quietSettingSpec registers the quiet-hours window with the settings
// overlay. The view reports the effective (defaulted) bounds so clients always
// see concrete times. The quietPublisher reads quietHoursWindow() per publish,
// so a change takes effect on the next sound.
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
