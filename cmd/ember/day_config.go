package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

type DayConfig struct {
	Enabled          *bool  `json:"enabled"`
	DefaultStop      string `json:"default_stop"`
	BreakHintMinutes *int   `json:"break_hint_minutes"`
	AutoStopMinutes  int    `json:"auto_stop_minutes"`
	GraceMinutes     *int   `json:"grace_minutes"`
}

const (
	dayDefaultStop      = "00:00"
	dayBreakHintDefault = 90
	dayAutoStopDefault  = 30
	dayGraceDefault     = 15
	daySettingsKey      = "day_json"
)

func (c DayConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

func (c DayConfig) breakHint() time.Duration {
	if c.BreakHintMinutes == nil {
		return dayBreakHintDefault * time.Minute
	}
	return time.Duration(*c.BreakHintMinutes) * time.Minute
}

func (c DayConfig) grace() time.Duration {
	if c.GraceMinutes == nil {
		return dayGraceDefault * time.Minute
	}
	return time.Duration(*c.GraceMinutes) * time.Minute
}

func (c DayConfig) autoStop() time.Duration {
	if c.AutoStopMinutes <= 0 {
		return dayAutoStopDefault * time.Minute
	}
	return time.Duration(c.AutoStopMinutes) * time.Minute
}

func (c DayConfig) defaultStop() string {
	if _, _, ok := workday.ParseHHMM(c.DefaultStop); ok {
		return c.DefaultStop
	}
	return dayDefaultStop
}

func defaultDayConfig() DayConfig {
	var c DayConfig
	c.fillAbsent()
	return c
}

func (c *DayConfig) fillAbsent() {
	if c.Enabled == nil {
		c.Enabled = boolPtr(true)
	}
	if c.DefaultStop == "" {
		c.DefaultStop = dayDefaultStop
	}
	if c.BreakHintMinutes == nil {
		c.BreakHintMinutes = intPtr(dayBreakHintDefault)
	}
	if c.AutoStopMinutes == 0 {
		c.AutoStopMinutes = dayAutoStopDefault
	}
	if c.GraceMinutes == nil {
		c.GraceMinutes = intPtr(dayGraceDefault)
	}
}

func validateDay(c DayConfig) error {
	if _, _, ok := workday.ParseHHMM(c.DefaultStop); !ok {
		return fmt.Errorf("%w: day.default_stop %q must be HH:MM", ErrConfigValidate, c.DefaultStop)
	}
	if c.BreakHintMinutes != nil && (*c.BreakHintMinutes < 0 || *c.BreakHintMinutes > 600) {
		return fmt.Errorf("%w: day.break_hint_minutes %d out of range [0, 600]", ErrConfigValidate, *c.BreakHintMinutes)
	}
	if c.AutoStopMinutes < 5 || c.AutoStopMinutes > 240 {
		return fmt.Errorf("%w: day.auto_stop_minutes %d out of range [5, 240]", ErrConfigValidate, c.AutoStopMinutes)
	}
	if c.GraceMinutes != nil && (*c.GraceMinutes < 0 || *c.GraceMinutes > 120) {
		return fmt.Errorf("%w: day.grace_minutes %d out of range [0, 120]", ErrConfigValidate, *c.GraceMinutes)
	}
	return nil
}

func (a *App) daySettingSpec() settingSpec[DayConfig] {
	return settingSpec[DayConfig]{
		key:  daySettingsKey,
		view: func(c Config) DayConfig { return c.Day },
		apply: func(c *Config, d DayConfig) error {
			d.fillAbsent()
			if err := validateDay(d); err != nil {
				return err
			}
			c.Day = d
			return nil
		},
		after: func(Config) { a.dayChanged() },
	}
}

func (a *App) handleDayConfigGet(w http.ResponseWriter, r *http.Request) {
	serveSettingGet(w, a.settings.day)
}

func (a *App) handleDayConfigPut(w http.ResponseWriter, r *http.Request) {
	if d, ok := serveSettingPut(a, w, r, a.settings.day); ok {
		writeJSON(w, http.StatusOK, d)
	}
}
