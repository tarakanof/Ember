package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

type settingSpec[D any] struct {
	key    string
	view   func(Config) D
	apply  func(*Config, D) error
	after  func(Config)
	encode func(D) string
	decode func(string) []byte
}

type setting[D any] struct {
	o    *settingsOverlay
	spec settingSpec[D]
}

type settingsOverlay struct {
	update func(func(*Config) error) error
	load   func() *Config
	kv     func() settingsKV
	logger *slog.Logger
	all    []interface{ reapply() }
}

type appSettings struct {
	*settingsOverlay
	pomodoro *setting[pomodoroSettingsDTO]
	weather  *setting[WeatherConfig]
	meetings *setting[MeetingsConfig]
	usage    *setting[usageConfigDTO]
	display  *setting[displayConfigDTO]
	quiet    *setting[quietConfigDTO]
	clock    *setting[clockConfigDTO]
}

func newAppSettings(a *App) appSettings {
	o := &settingsOverlay{
		update: a.tryUpdateConfig,
		load:   a.cfg.Load,
		kv: func() settingsKV {
			if a.store == nil {
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
		clock:           register(o, clockSettingSpec()),
	}
}

func register[D any](o *settingsOverlay, spec settingSpec[D]) *setting[D] {
	s := &setting[D]{o: o, spec: spec}
	o.all = append(o.all, s)
	return s
}

func (s *setting[D]) get() D { return s.spec.view(*s.o.load()) }

var errSettingBody = errors.New("invalid settings body")

var errSettingNotObject = fmt.Errorf("%w: must be a JSON object", errSettingBody)

func (s *setting[D]) put(patch []byte) (D, error) { return s.putWith(patch, nil) }

func (s *setting[D]) putWith(patch []byte, also func(*Config)) (D, error) {
	var next Config
	err := s.o.update(func(cur *Config) error {
		d, err := mergeSetting(s.spec.view(*cur), patch)
		if err != nil {
			return err
		}
		if err := s.spec.apply(cur, d); err != nil {
			return err
		}
		if also != nil {
			also(cur)
		}
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

func (s *setting[D]) persist(c Config) {
	kv := s.o.kv()
	if kv == nil {
		return
	}
	var blob string
	if s.spec.encode != nil {
		if blob = s.spec.encode(s.spec.view(c)); blob == "" {
			return
		}
	} else {
		b, err := json.Marshal(s.spec.view(c))
		if err != nil {
			s.o.logger.Warn("settings marshal failed", "key", s.spec.key, "err", err)
			return
		}
		blob = string(b)
	}
	if err := kv.PutSetting(s.spec.key, blob); err != nil {
		s.o.logger.Warn("settings persist failed", "key", s.spec.key, "err", err)
	}
}

func (s *setting[D]) reapply() {
	kv := s.o.kv()
	if kv == nil {
		return
	}
	blob, ok, err := kv.GetSetting(s.spec.key)
	if err != nil || !ok {
		return
	}
	patch := []byte(blob)
	if s.spec.decode != nil {
		patch = s.spec.decode(blob)
	}
	if _, err := s.put(patch); err != nil {
		s.o.logger.Warn("persisted settings ignored", "key", s.spec.key, "err", err)
	}
}

func (o *settingsOverlay) reapply() {
	for _, s := range o.all {
		s.reapply()
	}
}

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

func serveSettingGet[D any](w http.ResponseWriter, s *setting[D]) {
	writeJSON(w, http.StatusOK, s.get())
}

func serveSettingPut[D any](a *App, w http.ResponseWriter, r *http.Request, s *setting[D]) (D, bool) {
	return serveSettingPutWith(a, w, r, s, nil)
}

func serveSettingPutWith[D any](a *App, w http.ResponseWriter, r *http.Request, s *setting[D], also func(patch []byte) func(*Config)) (D, bool) {
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		var zero D
		return zero, false
	}
	var fn func(*Config)
	if also != nil {
		fn = also(patch)
	}
	d, err := s.putWith(patch, fn)
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
