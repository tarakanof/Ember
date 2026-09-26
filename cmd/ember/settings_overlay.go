package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// The settings overlay owns every runtime-editable slice of Config: the
// config.json file is the baseline, and a menu PUT stored in the settings KV
// wins over it until the next PUT. One module does the merge, validation,
// atomic swap, persistence and re-apply (startup and /admin/reload) for all of
// them; a feature only registers a settingSpec.
//
// Merge rule, for PUT bodies and stored blobs alike: the body is a JSON object
// laid over the current effective value key by key. An omitted key keeps its
// current value; a present key replaces the whole field (objects and maps
// included). So a partial PUT changes only what it names, and a blob written
// before a field existed keeps that field's current value on load.

// settingSpec is what a feature registers.
type settingSpec[D any] struct {
	// key is the settings-KV key the override persists under.
	key string
	// view maps the effective config to the wire DTO. It is the GET body,
	// the seed a PUT is merged onto, and the persisted blob, so it must
	// resolve every field (no JSON nulls for "default").
	view func(Config) D
	// apply writes a merged DTO into a copy of the config, or returns why it
	// is invalid (then nothing changes). Validation lives here.
	apply func(*Config, D) error
	// after, if set, runs once a change has landed (outside the config lock):
	// engine updates, re-render nudges, device provisioning.
	after func(Config)
}

// setting is a registered settingSpec bound to its overlay.
type setting[D any] struct {
	o    *settingsOverlay
	spec settingSpec[D]
}

// settingsOverlay holds the dependencies every setting shares and the
// registration order used by reapply.
type settingsOverlay struct {
	// update runs a validated read-copy-write of the live config
	// (App.tryUpdateConfig); load reads it.
	update func(func(*Config) error) error
	load   func() *Config
	// kv returns the settings store, or nil while none is open (the store
	// opens after NewApp and may fail to open; settings then live in memory).
	kv     func() settingsKV
	logger *slog.Logger
	all    []interface{ reapply() }
}

// appSettings is the App's overlay with every registered setting. The reapply
// order is the order of the register calls in newAppSettings.
type appSettings struct {
	*settingsOverlay
	pomodoro *setting[pomodoroSettingsDTO]
	weather  *setting[WeatherConfig]
	meetings *setting[MeetingsConfig]
	usage    *setting[usageConfigDTO]
	display  *setting[displayConfigDTO]
	quiet    *setting[quietConfigDTO]
}

// newAppSettings wires the overlay to a's live config and settings store and
// registers each feature.
func newAppSettings(a *App) appSettings {
	o := &settingsOverlay{
		update: a.tryUpdateConfig,
		load:   a.cfg.Load,
		kv: func() settingsKV {
			if a.store == nil { // a typed nil would slip past a nil-interface check
				return nil
			}
			return a.store
		},
		logger: a.logger,
	}
	return appSettings{
		settingsOverlay: o,
		pomodoro:        register(o, a.pomodoroSettingSpec()),
		weather:         register(o, a.weatherSettingSpec()),
		meetings:        register(o, a.meetingsSettingSpec()),
		usage:           register(o, a.usageSettingSpec()),
		display:         register(o, displaySettingSpec()),
		quiet:           register(o, quietSettingSpec()),
	}
}

// register binds spec to o and adds it to the reapply order.
func register[D any](o *settingsOverlay, spec settingSpec[D]) *setting[D] {
	s := &setting[D]{o: o, spec: spec}
	o.all = append(o.all, s)
	return s
}

// get returns the effective value.
func (s *setting[D]) get() D { return s.spec.view(*s.o.load()) }

// errSettingBody marks a patch that can't be decoded into the setting's DTO:
// not a JSON object, or a value of the wrong type. HTTP answers it like any
// other undecodable body (rejectBody); validation errors are separate.
var errSettingBody = errors.New("invalid settings body")

// errSettingNotObject rejects a patch that isn't a JSON object.
var errSettingNotObject = fmt.Errorf("%w: must be a JSON object", errSettingBody)

// put merges patch (a JSON object) over the effective value, validates and
// swaps the result in, persists it, and runs the after hook. It returns the
// new effective value, or an error (and changes nothing) when patch is not an
// object or the merged value is invalid.
func (s *setting[D]) put(patch []byte) (D, error) {
	var next Config
	err := s.o.update(func(cur *Config) error {
		d, err := mergeSetting(s.spec.view(*cur), patch)
		if err != nil {
			return err
		}
		if err := s.spec.apply(cur, d); err != nil {
			return err
		}
		// Persist inside the lock so a racing PUT can't overwrite the store
		// with a value older than the one left live.
		s.persist(*cur)
		next = *cur
		return nil
	})
	if err != nil {
		var zero D
		return zero, err
	}
	if s.spec.after != nil {
		s.spec.after(next)
	}
	return s.spec.view(next), nil
}

// persist writes the normalised view of c, never the raw request body.
func (s *setting[D]) persist(c Config) {
	kv := s.o.kv()
	if kv == nil {
		return
	}
	blob, err := json.Marshal(s.spec.view(c))
	if err != nil {
		s.o.logger.Warn("settings marshal failed", "key", s.spec.key, "err", err)
		return
	}
	if err := kv.PutSetting(s.spec.key, string(blob)); err != nil {
		s.o.logger.Warn("settings persist failed", "key", s.spec.key, "err", err)
	}
}

// reapply lays the stored override (if any) over the current config. An
// unreadable or invalid blob is logged and ignored, leaving the baseline.
func (s *setting[D]) reapply() {
	kv := s.o.kv()
	if kv == nil {
		return
	}
	blob, ok, err := kv.GetSetting(s.spec.key)
	if err != nil || !ok {
		return
	}
	if _, err := s.put([]byte(blob)); err != nil {
		s.o.logger.Warn("persisted settings ignored", "key", s.spec.key, "err", err)
	}
}

// reapply re-applies every stored override in registration order. Called at
// startup once the store is open, and after /admin/reload swaps in a fresh
// file baseline, so neither reverts a menu edit.
func (o *settingsOverlay) reapply() {
	for _, s := range o.all {
		s.reapply()
	}
}

// mergeSetting lays patch's top-level keys over seed's JSON form and decodes
// the result into a fresh D. Decoding into a fresh value (rather than onto
// seed) keeps pointer and map fields shared with the live config from being
// written through. A patch key replaces any seed key it matches
// case-insensitively, as encoding/json would match it to the same field.
func mergeSetting[D any](seed D, patch []byte) (D, error) {
	var out D
	var p map[string]json.RawMessage
	if err := json.Unmarshal(patch, &p); err != nil || p == nil {
		return out, errSettingNotObject
	}
	base, err := json.Marshal(seed)
	if err != nil {
		return out, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(base, &m); err != nil {
		return out, err
	}
	for k, v := range p {
		for mk := range m {
			if strings.EqualFold(mk, k) {
				delete(m, mk)
			}
		}
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(merged, &out); err != nil {
		return out, fmt.Errorf("%w: %w", errSettingBody, err)
	}
	return out, nil
}

// serveSettingGet answers GET with the effective value.
func serveSettingGet[D any](w http.ResponseWriter, s *setting[D]) {
	writeJSON(w, http.StatusOK, s.get())
}

// serveSettingPut decodes a PUT body and merges it through s. On success it
// returns the new effective value for the caller to write; otherwise it has
// already answered (413/400).
func serveSettingPut[D any](a *App, w http.ResponseWriter, r *http.Request, s *setting[D]) (D, bool) {
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		var zero D
		return zero, false
	}
	d, err := s.put(patch)
	if errors.Is(err, errSettingBody) {
		a.rejectBody(w, r, err)
		return d, false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return d, false
	}
	return d, true
}
