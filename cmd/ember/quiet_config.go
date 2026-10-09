package main

import (
	"fmt"
	"net/http"
	"slices"
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

func (c Config) quietAt(t time.Time) bool {
	enabled, start, end := c.quietHoursWindow()
	return enabled && quietActive(start, end, t)
}

func (c Config) nextQuietEdge(t time.Time) (time.Time, bool) {
	enabled, start, end := c.quietHoursWindow()
	if !enabled || start == end {
		return time.Time{}, false
	}
	limit := t.Add(72 * time.Hour)
	var cands []time.Time
	shifts := []time.Duration{0}
	for at := t.Add(-48 * time.Hour); ; {
		_, zoneEnd := at.ZoneBounds()
		if zoneEnd.IsZero() || !zoneEnd.Before(limit) {
			break
		}
		_, before := zoneEnd.Add(-time.Second).Zone()
		_, after := zoneEnd.Zone()
		d := time.Duration(after-before) * time.Second
		cands = append(cands, zoneEnd)
		shifts = append(shifts, d, -d)
		at = zoneEnd
	}
	for day := -1; day <= 3; day++ {
		for _, m := range [2]int{start, end} {
			w := time.Date(t.Year(), t.Month(), t.Day()+day, m/60, m%60, 0, 0, t.Location())
			for _, s := range shifts {
				cands = append(cands, w.Add(s))
			}
		}
	}
	slices.SortFunc(cands, time.Time.Compare)
	cur := c.quietAt(t)
	for _, x := range cands {
		if x.After(t) && c.quietAt(x) != cur {
			return x, true
		}
	}
	return time.Time{}, false
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
