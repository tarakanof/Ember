package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

const (
	clockConfigSchema       = 1
	clockRotationReadBudget = 2 * time.Second
	clockRotationOpBudget   = 11 * time.Second
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
	a.cfgMu.Lock()
	cfg := *a.cfg.Load()
	hidden := a.hiddenAppNames()
	a.cfgMu.Unlock()
	return clockConfig{
		Schema:   clockConfigSchema,
		Apps:     composeClockApps(cfg, hidden),
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

func clockConfigDigest(c clockConfig) string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *App) clockConfigVersion() int {
	if a.devices == nil {
		return 0
	}
	return a.devices.clockConfigVersion()
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
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" {
			return nil, fmt.Errorf("%w: apps.agents.hidden_tools names must not be empty", errSettingBody)
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
	r := &clockRotation{Order: []string{}, Disabled: []string{}}
	for _, app := range apps {
		switch {
		case app.Origin == "pushed":
		case app.Enabled:
			r.Order = append(r.Order, app.Name)
		default:
			r.Disabled = append(r.Disabled, app.Name)
		}
	}
	return r, nil
}

func rotationUnchanged(patch *deviceAppsPutBody, cur *clockRotation) bool {
	if cur == nil || !slices.Equal(patch.Order, cur.Order) {
		return false
	}
	want := slices.Sorted(slices.Values(patch.Disabled))
	have := slices.Sorted(slices.Values(cur.Disabled))
	return slices.Equal(slices.Compact(want), slices.Compact(have))
}

type clockRotationOp struct {
	slot   chan struct{}
	budget atomic.Int64
}

var (
	errClockRotationBusy    = errors.New("another clock app order change is still running")
	errClockRotationExpired = errors.New("the clock app order change ran out of time")
)

func (a *App) startClockRotationOp(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), time.Duration(a.rotationOp.budget.Load()))
}

func (a *App) acquireClockRotation(ctx context.Context, w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	select {
	case a.rotationOp.slot <- struct{}{}:
	case <-ctx.Done():
		a.rejectClockRotation(w, r, errClockRotationBusy)
		return nil, false
	}
	release = func() { <-a.rotationOp.slot }
	if ctx.Err() != nil {
		release()
		a.rejectClockRotation(w, r, errClockRotationExpired)
		return nil, false
	}
	return release, true
}

func (a *App) holdClockRotation() (release func()) {
	a.rotationOp.slot <- struct{}{}
	return func() { <-a.rotationOp.slot }
}

func (a *App) rejectClockRotation(w http.ResponseWriter, r *http.Request, reason error) {
	if r.Context().Err() != nil {
		a.logger.InfoContext(r.Context(), "clock app order request dropped: client went away while waiting", "path", r.URL.Path)
		return
	}
	writeError(w, http.StatusServiceUnavailable, reason)
}

func (a *App) forgetClockRotationAfter(err error) {
	var apiErr *awtrix.APIError
	if !errors.As(err, &apiErr) {
		a.setClockRotation(nil)
	}
}

func (a *App) setClockRotation(r *clockRotation) {
	a.clockRotation.Store(r)
	a.syncClockConfigVersion()
}

func (a *App) readClockRotation(ctx context.Context) (*clockRotation, error) {
	ctx, cancel := context.WithTimeout(ctx, clockRotationReadBudget)
	defer cancel()
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawApps)
	if err != nil {
		return nil, err
	}
	return rotationFromApps(body)
}

func (a *App) refreshClockRotation(ctx context.Context) {
	r, err := a.readClockRotation(ctx)
	if err != nil {
		a.logger.Debug("clock app list not read", "err", err)
		return
	}
	a.setClockRotation(r)
}

func (a *App) readBackClockRotation(ctx context.Context) *clockRotation {
	r, err := a.readClockRotation(ctx)
	if err != nil {
		a.logger.WarnContext(ctx, "clock app order written but not read back", "err", err)
		return nil
	}
	return r
}

var (
	errClockWrite      = errors.New("clock write failed")
	errSettingsPersist = errors.New("settings not stored")
)

func (a *App) putSettingsBatch(values map[string]string) error {
	if a.store == nil || len(values) == 0 {
		return nil
	}
	return a.store.PutSettings(values)
}

type clockPutResult struct {
	rotationWritten bool
}

func (a *App) putClockConfig(ctx context.Context, patch []byte) (clockPutResult, error) {
	var res clockPutResult
	cur := a.composeClockConfig()
	next, err := mergeClockConfig(cur, patch)
	if err != nil {
		return res, err
	}
	dry := *a.cfg.Load()
	if _, err := a.stageClockApps(&dry, next.Apps); err != nil {
		return res, err
	}
	var rotation *clockRotation
	if next.Rotation != nil {
		pre, err := a.readClockRotation(ctx)
		if err == nil && rotationUnchanged(next.Rotation, pre) {
			rotation = pre
		} else {
			payload, _ := json.Marshal(next.Rotation)
			if _, err := a.clock.fetch(ctx, withBody((*awtrix.Client).RawPutAppOrder, payload)); err != nil {
				a.forgetClockRotationAfter(err)
				return res, fmt.Errorf("%w: %w", errClockWrite, err)
			}
			res.rotationWritten = true
			rotation = a.readBackClockRotation(ctx)
		}
	}
	a.pauseClockSync()
	defer a.resumeClockSync()
	if next.Rotation != nil {
		a.clockRotation.Store(rotation)
	}
	if a.commitHook != nil {
		a.commitHook()
	}
	var staged []*stagedSetting
	hiddenChanged := false
	err = a.tryUpdateConfig(func(c *Config) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", errClockRotationExpired, err)
		}
		a.appsMu.Lock()
		hidden := a.hiddenAppNamesLocked()
		a.appsMu.Unlock()
		n, err := mergeClockConfig(clockConfig{Schema: clockConfigSchema, Apps: composeClockApps(*c, hidden)}, patch)
		if err != nil {
			return err
		}
		before := *c
		if staged, err = a.stageClockApps(c, n.Apps); err != nil {
			return err
		}
		hiddenChanged = !slices.Equal(n.Apps.Agents.HiddenTools, hidden)
		if len(staged) == 0 && !hiddenChanged {
			return errNoChange
		}
		batch := make(map[string]string, len(staged)+2)
		for _, s := range staged {
			blob, ok := s.blob(*c)
			if prev, _ := s.blob(before); ok && (c.clockPresentation == nil || blob != prev) {
				batch[s.key] = blob
			}
		}
		if captureClockPresentation(before, c) {
			batch[clockConfigKey] = clockPresentationBlob(c.clockPresentation)
		}
		if hiddenChanged {
			blob, _ := json.Marshal(n.Apps.Agents.HiddenTools)
			batch[hiddenAppsKey] = string(blob)
		}
		if err := a.putSettingsBatch(batch); err != nil {
			return fmt.Errorf("%w: %w", errSettingsPersist, err)
		}
		if hiddenChanged {
			a.setHiddenAppsMemory(n.Apps.Agents.HiddenTools)
		}
		return nil
	})
	if errors.Is(err, errNoChange) {
		return res, nil
	}
	if err != nil {
		return res, err
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
	return res, nil
}

func (a *App) handleClockConfigGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.startClockRotationOp(r)
	defer cancel()
	release, ok := a.acquireClockRotation(ctx, w, r)
	if !ok {
		return
	}
	defer release()
	a.refreshClockRotation(ctx)
	a.syncClockConfigVersion()
	version := a.clockConfigVersion()
	cfg := a.composeClockConfig()
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleClockConfigPut(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := a.startClockRotationOp(r)
	defer cancel()
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		return
	}
	release, ok := a.acquireClockRotation(ctx, w, r)
	if !ok {
		return
	}
	defer release()
	before := a.clockConfigVersion()
	res, err := a.putClockConfig(ctx, patch)
	if err != nil {
		if res.rotationWritten {
			a.logger.WarnContext(r.Context(), "clock app order written, settings not applied", "device_id", id, "err", err)
			err = fmt.Errorf("%w (the clock's app order was already written)", err)
		}
		switch {
		case errors.Is(err, errClockWrite):
			writeClockError(w, err)
		case errors.Is(err, errClockRotationExpired):
			writeError(w, http.StatusServiceUnavailable, err)
		case errors.Is(err, errSettingBody):
			a.writeDeviceError(w, r, err)
		case errors.Is(err, errSettingsPersist):
			a.logger.WarnContext(r.Context(), "device config not stored", "device_id", id, "err", err)
			writeError(w, http.StatusInternalServerError, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	version := a.clockConfigVersion()
	cfg := a.composeClockConfig()
	if version != before {
		a.logger.InfoContext(r.Context(), "device config updated", "device_id", id, "config_version", version)
	}
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}
