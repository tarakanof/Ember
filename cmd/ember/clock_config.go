package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/awtrix"
)

const (
	clockConfigSchema       = 1
	clockHiddenToolsMax     = 64
	clockRotationReadBudget = 2 * time.Second
)

type clockConfig struct {
	Schema   int            `json:"schema"`
	Apps     clockApps      `json:"apps"`
	Rotation *clockRotation `json:"rotation"`
}

type clockApps struct {
	Agents   clockAgentsApp   `json:"agents"`
	Focus    clockFocusApp    `json:"focus"`
	Weather  clockWeatherApp  `json:"weather"`
	Calendar clockCalendarApp `json:"calendar"`
}

type clockAgentsApp struct {
	UsageCards    bool     `json:"usage_cards"`
	UsagePerModel bool     `json:"usage_per_model"`
	HiddenTools   []string `json:"hidden_tools"`
}

type clockFocusApp struct {
	FocusColor string `json:"focus_color"`
	BreakColor string `json:"break_color"`
}

type clockWeatherApp struct {
	On            bool               `json:"on"`
	NativeIcon    bool               `json:"native_icon"`
	Forecast      bool               `json:"forecast"`
	ForecastHours int                `json:"forecast_hours"`
	Air           bool               `json:"air"`
	Moon          bool               `json:"moon"`
	Overlay       bool               `json:"overlay"`
	Popups        clockWeatherPopups `json:"popups"`
	IconIDs       map[string]string  `json:"icon_ids"`
}

type clockWeatherPopups struct {
	OnChange        bool `json:"on_change"`
	Sun             bool `json:"sun"`
	Severe          bool `json:"severe"`
	NativeIcons     bool `json:"native_icons"`
	IntervalMinutes int  `json:"interval_minutes"`
	DurationSeconds int  `json:"duration_seconds"`
}

type clockCalendarApp struct {
	On               bool `json:"on"`
	TileLeadMinutes  int  `json:"tile_lead_minutes"`
	PopupLeadMinutes int  `json:"popup_lead_minutes"`
}

type clockRotation struct {
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}

func composeClockApps(c Config, hidden []string) clockApps {
	w, m := c.Weather, c.Meetings
	if hidden == nil {
		hidden = []string{}
	}
	icons := maps.Clone(w.IconIDs)
	if icons == nil {
		icons = map[string]string{}
	}
	return clockApps{
		Agents: clockAgentsApp{
			UsageCards:    c.usageWidgetEnabled(),
			UsagePerModel: c.usagePerModelEnabled(),
			HiddenTools:   hidden,
		},
		Focus: clockFocusApp{FocusColor: c.Pomodoro.FocusColor, BreakColor: c.Pomodoro.BreakColor},
		Weather: clockWeatherApp{
			On:            w.RotateInAppsEnabled(),
			NativeIcon:    w.TileNativeIcons,
			Forecast:      w.ForecastTileEnabled(),
			ForecastHours: w.ForecastHours,
			Air:           w.AirTileEnabled(),
			Moon:          w.MoonPhaseEnabled(),
			Overlay:       w.OverlayEnabled(),
			Popups: clockWeatherPopups{
				OnChange:        w.PopupOnChangeEnabled(),
				Sun:             w.SunPopupsEnabled(),
				Severe:          w.SevereAlertEnabled(),
				NativeIcons:     w.UseNativeIcons,
				IntervalMinutes: w.PopupIntervalMins(),
				DurationSeconds: w.PopupDurationSeconds,
			},
			IconIDs: icons,
		},
		Calendar: clockCalendarApp{
			On:               m.IsEnabled(),
			TileLeadMinutes:  m.TileLeadMinutes,
			PopupLeadMinutes: m.PopupLeadMins(),
		},
	}
}

func (a *App) composeClockConfig() clockConfig {
	return clockConfig{
		Schema:   clockConfigSchema,
		Apps:     composeClockApps(*a.cfg.Load(), a.hiddenAppNames()),
		Rotation: a.clockRotation.Load(),
	}
}

func clockConfigHash(c clockConfig) int {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	v := int(binary.BigEndian.Uint32(sum[:4]) & 0x7fffffff)
	if v == 0 {
		return 1
	}
	return v
}

func (a *App) clockConfigVersion() int {
	return clockConfigHash(a.composeClockConfig())
}

func (a *App) stageClockApps(cur *Config, apps clockApps) ([]*stagedSetting, error) {
	s := a.settings
	edits := []func() (*stagedSetting, error){
		func() (*stagedSetting, error) {
			return s.usage.stage(cur, func(d *usageConfigDTO) {
				d.UsageWidget = apps.Agents.UsageCards
				d.UsagePerModel = apps.Agents.UsagePerModel
			})
		},
		func() (*stagedSetting, error) {
			return s.pomodoro.stage(cur, func(d *pomodoroSettingsDTO) {
				d.FocusColor = &apps.Focus.FocusColor
				d.BreakColor = &apps.Focus.BreakColor
			})
		},
		func() (*stagedSetting, error) {
			return s.weather.stage(cur, func(d *WeatherConfig) {
				w := apps.Weather
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
			})
		},
		func() (*stagedSetting, error) {
			return s.meetings.stage(cur, func(d *MeetingsConfig) {
				c := apps.Calendar
				d.Enabled = boolPtr(c.On)
				d.TileLeadMinutes = c.TileLeadMinutes
				d.PopupLeadMinutes = intPtr(c.PopupLeadMinutes)
			})
		},
	}
	var staged []*stagedSetting
	for _, edit := range edits {
		st, err := edit()
		if err != nil {
			return nil, err
		}
		if st != nil {
			staged = append(staged, st)
		}
	}
	return staged, nil
}

func normalizeHiddenTools(names []string) ([]string, error) {
	if len(names) > clockHiddenToolsMax {
		return nil, fmt.Errorf("%w: apps.agents.hidden_tools holds at most %d names", errSettingBody, clockHiddenToolsMax)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || !utf8.ValidString(n) || utf8.RuneCountInString(n) > 64 {
			return nil, fmt.Errorf("%w: apps.agents.hidden_tools names must be 1-64 characters", errSettingBody)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

type clockConfigPatch struct {
	Schema   int                `json:"schema"`
	Apps     clockApps          `json:"apps"`
	Rotation *deviceAppsPutBody `json:"rotation"`
}

func mergeClockConfig(cur clockConfig, patch []byte) (clockConfigPatch, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil || keys == nil {
		return clockConfigPatch{}, errSettingNotObject
	}
	var probe struct {
		Apps struct {
			Weather struct {
				IconIDs json.RawMessage `json:"icon_ids"`
			} `json:"weather"`
		} `json:"apps"`
	}
	_ = json.Unmarshal(patch, &probe)
	next := clockConfigPatch{Schema: cur.Schema, Apps: cur.Apps}
	next.Apps.Agents.HiddenTools = slices.Clone(cur.Apps.Agents.HiddenTools)
	next.Apps.Weather.IconIDs = maps.Clone(cur.Apps.Weather.IconIDs)
	if len(probe.Apps.Weather.IconIDs) > 0 {
		next.Apps.Weather.IconIDs = nil
	}
	dec := json.NewDecoder(bytes.NewReader(patch))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return clockConfigPatch{}, fmt.Errorf("%w: %w", errSettingBody, err)
	}
	if next.Schema != clockConfigSchema {
		return clockConfigPatch{}, fmt.Errorf("%w: schema must be %d", errSettingBody, clockConfigSchema)
	}
	hidden, err := normalizeHiddenTools(next.Apps.Agents.HiddenTools)
	if err != nil {
		return clockConfigPatch{}, err
	}
	next.Apps.Agents.HiddenTools = hidden
	if next.Apps.Weather.IconIDs == nil {
		next.Apps.Weather.IconIDs = map[string]string{}
	}
	return next, nil
}

func rotationFromApps(body []byte) (*clockRotation, error) {
	var apps []awtrix.AppInfo
	if err := json.Unmarshal(body, &apps); err != nil {
		return nil, fmt.Errorf("decode clock apps: %w", err)
	}
	r := &clockRotation{Order: make([]string, 0, len(apps)), Disabled: []string{}}
	for _, app := range apps {
		r.Order = append(r.Order, app.Name)
		if !app.Enabled {
			r.Disabled = append(r.Disabled, app.Name)
		}
	}
	return r, nil
}

func (a *App) noteClockApps(body []byte) {
	r, err := rotationFromApps(body)
	if err != nil {
		a.logger.Debug("clock app list not cached", "err", err)
		return
	}
	a.clockRotation.Store(r)
	a.syncClockConfigVersion()
}

func (a *App) forgetClockRotation() {
	if a.clockRotation.Swap(nil) != nil {
		a.syncClockConfigVersion()
	}
}

func (a *App) refreshClockRotation(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, clockRotationReadBudget)
	defer cancel()
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawApps)
	if err != nil {
		a.logger.Debug("clock app list not read", "err", err)
		return
	}
	a.noteClockApps(body)
}

var errClockWrite = errors.New("clock write failed")

func (a *App) putClockConfig(ctx context.Context, patch []byte) error {
	a.clockSync.paused.Add(1)
	defer func() {
		a.clockSync.paused.Add(-1)
		a.syncClockConfigVersion()
	}()
	cur := a.composeClockConfig()
	next, err := mergeClockConfig(cur, patch)
	if err != nil {
		return err
	}
	dry := *a.cfg.Load()
	pending, err := a.stageClockApps(&dry, next.Apps)
	if err != nil {
		return err
	}
	if next.Rotation != nil {
		payload, _ := json.Marshal(next.Rotation)
		if _, err := a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload)); err != nil {
			return fmt.Errorf("%w: %w", errClockWrite, err)
		}
		a.refreshClockRotation(ctx)
	}
	hiddenChanged := !slices.Equal(next.Apps.Agents.HiddenTools, cur.Apps.Agents.HiddenTools)
	if len(pending) == 0 && !hiddenChanged {
		return nil
	}
	var staged []*stagedSetting
	err = a.tryUpdateConfig(func(c *Config) error {
		var err error
		if staged, err = a.stageClockApps(c, next.Apps); err != nil {
			return err
		}
		for _, s := range staged {
			s.persist(*c)
		}
		if hiddenChanged {
			a.replaceHiddenApps(next.Apps.Agents.HiddenTools)
		}
		return nil
	})
	if err != nil {
		return err
	}
	final := *a.cfg.Load()
	for _, s := range staged {
		if s.after != nil {
			s.after(final)
		}
	}
	if hiddenChanged {
		a.nudgePomo()
	}
	return nil
}

func (a *App) handleClockConfigGet(w http.ResponseWriter, r *http.Request) {
	a.refreshClockRotation(r.Context())
	cfg := a.composeClockConfig()
	w.Header().Set(deviceConfigVersion, strconv.Itoa(clockConfigHash(cfg)))
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleClockConfigPut(w http.ResponseWriter, r *http.Request, id string, patch []byte) {
	before := a.clockConfigVersion()
	if err := a.putClockConfig(r.Context(), patch); err != nil {
		switch {
		case errors.Is(err, errClockWrite):
			writeClockError(w, err)
		case errors.Is(err, errSettingBody):
			a.writeDeviceError(w, r, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	cfg := a.composeClockConfig()
	version := clockConfigHash(cfg)
	if version != before {
		a.logger.InfoContext(r.Context(), "device config updated", "device_id", id, "config_version", version)
	}
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}
