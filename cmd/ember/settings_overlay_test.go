package main

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
)

// mapKV is the in-memory settingsKV adapter used by the overlay tests.
type mapKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *mapKV) GetSetting(key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *mapKV) PutSetting(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}

// toyDTO exercises the merge rules over three Config fields of different
// shapes: an int, a string and a map.
type toyDTO struct {
	A int               `json:"a"`
	B string            `json:"b"`
	M map[string]string `json:"m,omitempty"`
}

var errToyNegative = errors.New("a must be >= 0")

func toySpec(afterCalls *int) settingSpec[toyDTO] {
	return settingSpec[toyDTO]{
		key: "toy_json",
		view: func(c Config) toyDTO {
			return toyDTO{A: c.Display.IdleRestoreSeconds, B: c.QuietHours.Start, M: c.Weather.IconIDs}
		},
		apply: func(c *Config, d toyDTO) error {
			if d.A < 0 {
				return errToyNegative
			}
			c.Display.IdleRestoreSeconds, c.QuietHours.Start, c.Weather.IconIDs = d.A, d.B, d.M
			return nil
		},
		after: func(Config) {
			if afterCalls != nil {
				*afterCalls++
			}
		},
	}
}

// newTestOverlay builds an overlay over a standalone config and kv, the same
// wiring App uses (tryUpdateConfig semantics) without the rest of App.
func newTestOverlay(base Config, kv *mapKV) (*settingsOverlay, *atomic.Pointer[Config]) {
	var cfg atomic.Pointer[Config]
	cfg.Store(&base)
	var mu sync.Mutex
	o := &settingsOverlay{
		update: func(mutate func(*Config) error) error {
			mu.Lock()
			defer mu.Unlock()
			cur := *cfg.Load()
			if err := mutate(&cur); err != nil {
				return err
			}
			cfg.Store(&cur)
			return nil
		},
		load:   cfg.Load,
		kv:     func() settingsKV { return kv },
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return o, &cfg
}

func toyBase() Config {
	var c Config
	c.Display.IdleRestoreSeconds = 7
	c.QuietHours.Start = "base"
	c.Weather.IconIDs = map[string]string{"rain": "1", "snow": "2"}
	return c
}

func TestSettingPutMergesOmittedFieldsUnchanged(t *testing.T) {
	kv := &mapKV{}
	var afters int
	o, cfg := newTestOverlay(toyBase(), kv)
	s := register(o, toySpec(&afters))

	got, err := s.put([]byte(`{"b":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.A != 7 || got.B != "new" || got.M["rain"] != "1" {
		t.Fatalf("put result = %+v, want a kept, b changed, m kept", got)
	}
	if c := cfg.Load(); c.Display.IdleRestoreSeconds != 7 || c.QuietHours.Start != "new" {
		t.Fatalf("live config = %+v / %+v", c.Display, c.QuietHours)
	}
	if v, _, _ := kv.GetSetting("toy_json"); v != `{"a":7,"b":"new","m":{"rain":"1","snow":"2"}}` {
		t.Fatalf("persisted = %s, want the full normalised view", v)
	}
	if afters != 1 {
		t.Fatalf("after ran %d times, want 1", afters)
	}
}

func TestSettingPutReplacesWholeMapWithoutAliasing(t *testing.T) {
	base := toyBase()
	shared := base.Weather.IconIDs
	o, cfg := newTestOverlay(base, &mapKV{})
	s := register(o, toySpec(nil))

	if _, err := s.put([]byte(`{"m":{"fog":"9"}}`)); err != nil {
		t.Fatal(err)
	}
	if m := cfg.Load().Weather.IconIDs; len(m) != 1 || m["fog"] != "9" {
		t.Fatalf("a present key replaces the whole map, got %v", m)
	}
	if len(shared) != 2 || shared["fog"] != "" {
		t.Fatalf("the previous config's map was written through: %v", shared)
	}
}

func TestSettingPutInvalidChangesNothing(t *testing.T) {
	kv := &mapKV{}
	var afters int
	o, cfg := newTestOverlay(toyBase(), kv)
	s := register(o, toySpec(&afters))

	for _, body := range []string{`{"a":-1,"b":"x"}`, `null`, `[1]`, `{"a":"text"}`} {
		if _, err := s.put([]byte(body)); err == nil {
			t.Fatalf("put(%s) succeeded, want an error", body)
		}
	}
	if c := cfg.Load(); c.Display.IdleRestoreSeconds != 7 || c.QuietHours.Start != "base" {
		t.Fatalf("invalid put changed the config: %+v", c.QuietHours)
	}
	if _, ok, _ := kv.GetSetting("toy_json"); ok || afters != 0 {
		t.Fatalf("invalid put persisted (%v) or ran after (%d)", ok, afters)
	}
}

func TestSettingPutKeyMatchIsCaseInsensitive(t *testing.T) {
	o, _ := newTestOverlay(toyBase(), &mapKV{})
	s := register(o, toySpec(nil))
	got, err := s.put([]byte(`{"B":"upper"}`))
	if err != nil || got.B != "upper" {
		t.Fatalf("put(B) = %+v, %v; want b=upper as encoding/json would decode it", got, err)
	}
}

func TestSettingsReapplyLaysStoredBlobsOverBaseline(t *testing.T) {
	kv := &mapKV{}
	// A legacy blob without "a" keeps the baseline value of a.
	_ = kv.PutSetting("toy_json", `{"b":"stored"}`)
	var afters int
	o, cfg := newTestOverlay(toyBase(), kv)
	register(o, toySpec(&afters))
	o.reapply()
	if c := cfg.Load(); c.Display.IdleRestoreSeconds != 7 || c.QuietHours.Start != "stored" {
		t.Fatalf("reapply: %+v / %+v", c.Display, c.QuietHours)
	}
	if afters != 1 {
		t.Fatalf("after ran %d times on reapply, want 1", afters)
	}
}

func TestSettingsReapplyIgnoresInvalidOrMissingBlob(t *testing.T) {
	for _, blob := range []string{`{"a":-5}`, `not json`} {
		kv := &mapKV{}
		_ = kv.PutSetting("toy_json", blob)
		o, cfg := newTestOverlay(toyBase(), kv)
		register(o, toySpec(nil))
		o.reapply()
		if c := cfg.Load(); c.Display.IdleRestoreSeconds != 7 || c.QuietHours.Start != "base" {
			t.Fatalf("blob %q changed the baseline", blob)
		}
	}
	// No stored override: nothing happens, including no after hook.
	var afters int
	o, _ := newTestOverlay(toyBase(), &mapKV{})
	register(o, toySpec(&afters))
	o.reapply()
	if afters != 0 {
		t.Fatalf("after ran without a stored override")
	}
}

func TestSettingWithoutStoreStaysInMemory(t *testing.T) {
	o, cfg := newTestOverlay(toyBase(), &mapKV{})
	o.kv = func() settingsKV { return nil }
	s := register(o, toySpec(nil))
	if _, err := s.put([]byte(`{"a":3}`)); err != nil {
		t.Fatal(err)
	}
	o.reapply() // no store: a no-op, not a panic
	if cfg.Load().Display.IdleRestoreSeconds != 3 || s.get().A != 3 {
		t.Fatalf("in-memory put not applied")
	}
}
