package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
)

const clockConfigKey = "clock_config_json"

type clockStoredConfig struct {
	Schema              int             `json:"schema"`
	Apps                clockStoredApps `json:"apps"`
	MigratedFromOverlay string          `json:"migrated_from_overlay"`
}

type clockStoredApps struct {
	Agents   *clockAgentsCards   `json:"agents,omitempty"`
	Focus    *clockFocusApp      `json:"focus,omitempty"`
	Weather  *clockWeatherApp    `json:"weather,omitempty"`
	Calendar *clockCalendarLeads `json:"calendar,omitempty"`
}

type clockAgentsCards struct {
	UsageCards    bool `json:"usage_cards"`
	UsagePerModel bool `json:"usage_per_model"`
}

type clockCalendarLeads struct {
	TileLeadMinutes  int `json:"tile_lead_minutes"`
	PopupLeadMinutes int `json:"popup_lead_minutes"`
}

var (
	weatherPresentationKeys = []string{
		"rotate_in_apps", "tile_native_icons", "forecast_tile", "forecast_hours", "air_tile", "moon_phase", "overlay",
		"popup_on_change", "sun_popups", "severe_alert", "use_native_icons", "popup_interval_minutes",
		"popup_duration_seconds", "icon_ids",
	}
	meetingsPresentationKeys = []string{"tile_lead_minutes", "popup_lead_minutes"}
	usagePresentationKeys    = []string{"usage_widget", "usage_per_model"}
	pomodoroPresentationKeys = []string{"focus_color", "break_color"}
)

func (s *clockStoredConfig) clone() *clockStoredConfig {
	if s == nil {
		return nil
	}
	c := *s
	c.Apps = s.Apps.clone()
	return &c
}

func (s clockStoredApps) clone() clockStoredApps {
	out := clockStoredApps{}
	if s.Agents != nil {
		v := *s.Agents
		out.Agents = &v
	}
	if s.Focus != nil {
		v := *s.Focus
		out.Focus = &v
	}
	if s.Weather != nil {
		v := *s.Weather
		v.IconIDs = maps.Clone(v.IconIDs)
		out.Weather = &v
	}
	if s.Calendar != nil {
		v := *s.Calendar
		out.Calendar = &v
	}
	return out
}

func (s clockStoredApps) names() []string {
	var out []string
	if s.Agents != nil {
		out = append(out, "agents")
	}
	if s.Focus != nil {
		out = append(out, "focus")
	}
	if s.Weather != nil {
		out = append(out, "weather")
	}
	if s.Calendar != nil {
		out = append(out, "calendar")
	}
	return out
}

func clockPresentationOf(c Config) clockStoredApps {
	apps := composeClockApps(c, nil)
	return clockStoredApps{
		Agents:   &clockAgentsCards{UsageCards: apps.Agents.UsageCards, UsagePerModel: apps.Agents.UsagePerModel},
		Focus:    &apps.Focus,
		Weather:  &apps.Weather,
		Calendar: &clockCalendarLeads{TileLeadMinutes: apps.Calendar.TileLeadMinutes, PopupLeadMinutes: apps.Calendar.PopupLeadMinutes},
	}
}

func (s clockStoredApps) applyTo(c *Config) {
	if a := s.Agents; a != nil {
		c.UsageWidget = boolPtr(a.UsageCards)
		c.UsagePerModel = boolPtr(a.UsagePerModel)
	}
	if f := s.Focus; f != nil {
		c.Pomodoro.FocusColor = f.FocusColor
		c.Pomodoro.BreakColor = f.BreakColor
	}
	if w := s.Weather; w != nil {
		d := &c.Weather
		d.RotateInApps = boolPtr(w.On)
		d.TileNativeIcons = w.NativeIcon
		d.ForecastTile = boolPtr(w.Forecast)
		d.ForecastHours = w.ForecastHours
		d.AirTile = boolPtr(w.Air)
		d.MoonPhase = boolPtr(w.Moon)
		d.Overlay = boolPtr(w.Overlay)
		d.PopupOnChange = boolPtr(w.Popups.OnChange)
		d.SunPopups = boolPtr(w.Popups.Sun)
		d.SevereAlert = boolPtr(w.Popups.Severe)
		d.UseNativeIcons = w.Popups.NativeIcons
		d.PopupIntervalMinutes = intPtr(w.Popups.IntervalMinutes)
		d.PopupDurationSeconds = w.Popups.DurationSeconds
		d.IconIDs = maps.Clone(w.IconIDs)
		if len(d.IconIDs) == 0 {
			d.IconIDs = nil
		}
	}
	if m := s.Calendar; m != nil {
		c.Meetings.TileLeadMinutes = m.TileLeadMinutes
		c.Meetings.PopupLeadMinutes = intPtr(m.PopupLeadMinutes)
	}
}

func validateClockPresentation(c Config) error {
	if err := validateWeather(c.Weather); err != nil {
		return err
	}
	if err := validateMeetings(c.Meetings); err != nil {
		return err
	}
	if !isHexColor(c.Pomodoro.FocusColor) || !isHexColor(c.Pomodoro.BreakColor) {
		return errors.New("apps.focus colours must be #RRGGBB")
	}
	return nil
}

func captureClockPresentation(before Config, c *Config) bool {
	if c.clockPresentation == nil {
		return false
	}
	was, now := clockPresentationOf(before), clockPresentationOf(*c)
	next := c.clockPresentation.clone()
	changed := false
	if !reflect.DeepEqual(was.Agents, now.Agents) {
		next.Apps.Agents, changed = now.Agents, true
	}
	if !reflect.DeepEqual(was.Focus, now.Focus) {
		next.Apps.Focus, changed = now.Focus, true
	}
	if !reflect.DeepEqual(was.Weather, now.Weather) {
		next.Apps.Weather, changed = now.Weather, true
	}
	if !reflect.DeepEqual(was.Calendar, now.Calendar) {
		next.Apps.Calendar, changed = now.Calendar, true
	}
	if changed {
		c.clockPresentation = next
	}
	return changed
}

func clockPresentationBlob(s *clockStoredConfig) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func carryClockPresentation(old Config, next *Config) {
	next.clockPresentation = old.clockPresentation.clone()
	if next.clockPresentation != nil {
		next.clockPresentation.Apps.applyTo(next)
	}
}

func (a *App) clockPresentationSettingSpec() settingSpec[clockStoredConfig] {
	return settingSpec[clockStoredConfig]{
		key: clockConfigKey,
		view: func(c Config) clockStoredConfig {
			if c.clockPresentation == nil {
				return clockStoredConfig{}
			}
			return *c.clockPresentation.clone()
		},
		apply: func(c *Config, d clockStoredConfig) error {
			if d.Schema != clockConfigSchema {
				return fmt.Errorf("schema must be %d", clockConfigSchema)
			}
			next := *c
			d.Apps = d.Apps.clone()
			d.Apps.applyTo(&next)
			if err := validateClockPresentation(next); err != nil {
				return err
			}
			next.clockPresentation = &d
			*c = next
			return nil
		},
		after: func(Config) { a.nudgePomo() },
	}
}

func dropKeys(blob []byte, keys []string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blob, &m); err != nil || m == nil {
		return blob
	}
	for _, k := range keys {
		delete(m, k)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return blob
	}
	return out
}

func hasAnyKey(blob string, keys []string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

type clockMigration struct {
	mu     sync.Mutex
	logged bool
	err    error
}

func (m *clockMigration) fail(err error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
	first := !m.logged
	m.logged = true
	return first
}

func (m *clockMigration) done() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = nil
}

func (m *clockMigration) lastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

var errClockRowUnread = errors.New("clock_config_json is stored but did not load; not overwriting it")

type presentationSlice struct {
	key  string
	keys []string
	blob func(Config) (string, bool)
	take func(dst *clockStoredApps, eff clockStoredApps)
}

func (a *App) presentationSlices() []presentationSlice {
	s := a.settings
	return []presentationSlice{
		{usageSettingsKey, usagePresentationKeys, s.usage.blob, func(d *clockStoredApps, e clockStoredApps) { d.Agents = e.Agents }},
		{pomodoroSettingsKey, pomodoroPresentationKeys, s.pomodoro.blob, func(d *clockStoredApps, e clockStoredApps) { d.Focus = e.Focus }},
		{weatherSettingsKey, weatherPresentationKeys, s.weather.blob, func(d *clockStoredApps, e clockStoredApps) { d.Weather = e.Weather }},
		{meetingsSettingsKey, meetingsPresentationKeys, s.meetings.blob, func(d *clockStoredApps, e clockStoredApps) { d.Calendar = e.Calendar }},
	}
}

func (a *App) migrateClockConfig() {
	if a.store == nil || a.devices == nil || !a.devices.hasClock() {
		return
	}
	release := a.holdClockRotation()
	defer release()
	var migrated *clockStoredConfig
	err := a.tryUpdateConfig(func(c *Config) error {
		if c.clockPresentation != nil {
			return errNoChange
		}
		if _, ok, err := a.store.GetSetting(clockConfigKey); err != nil {
			return err
		} else if ok {
			return errClockRowUnread
		}
		next := &clockStoredConfig{Schema: clockConfigSchema, MigratedFromOverlay: a.versionInfo.Version}
		eff := clockPresentationOf(*c)
		var rows []presentationSlice
		for _, s := range a.presentationSlices() {
			blob, ok, err := a.store.GetSetting(s.key)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			rows = append(rows, s)
			if hasAnyKey(blob, s.keys) {
				s.take(&next.Apps, eff)
			}
		}
		c.clockPresentation = next
		batch := map[string]string{clockConfigKey: clockPresentationBlob(next)}
		for _, s := range rows {
			if blob, ok := s.blob(*c); ok {
				batch[s.key] = blob
			}
		}
		if err := a.putSettingsBatch(batch); err != nil {
			return err
		}
		migrated = next
		return nil
	})
	switch {
	case errors.Is(err, errNoChange):
	case err != nil:
		if a.clockMigrate.fail(err) {
			a.logger.Warn("clock config not migrated; presentation stays in the source slices", "err", err)
		}
	default:
		a.clockMigrate.done()
		a.logger.Info("clock config migrated", "apps", migrated.Apps.names(), "migrated_from_overlay", migrated.MigratedFromOverlay)
	}
}
