package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
)

type capsCache struct {
	mu      sync.Mutex
	gen     uint64
	base    string
	value   *awtrix.Capabilities
	failing string
}

func (c *capsCache) begin() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *capsCache) put(gen uint64, base string, v *awtrix.Capabilities) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return false
	}
	c.gen++
	c.base, c.value, c.failing = base, v, ""
	return true
}

func (c *capsCache) fail(gen uint64, base string) (stored, first bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return false, false
	}
	c.gen++
	c.base, c.value = base, nil
	first = c.failing == "" || !sameDeviceURL(c.failing, base)
	c.failing = base
	return true, first
}

func (c *capsCache) forgetOutage() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failing = ""
}

func (c *capsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.base, c.value, c.failing = "", nil, ""
}

func (c *capsCache) get(base string) (awtrix.Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil || !sameDeviceURL(c.base, base) {
		return awtrix.Capabilities{}, false
	}
	return *c.value, true
}

func (a *App) refreshCapabilities(ctx context.Context) {
	gen := a.caps.begin()
	base := a.cfg.Load().effectiveClockURL()
	cl, err := a.clock.client(callCapabilities)
	if errors.Is(err, errClockNotConfigured) || errors.Is(err, errClockDisabled) {
		a.caps.forgetOutage()
		return
	}
	if err != nil {
		a.capsFetchFailed(gen, base, err)
		return
	}
	if info, err := cl.DeviceInfo(ctx); err == nil {
		a.deviceVersion.Store(info.Version)
		uid := ""
		if info.BoardType == discovery.NGBoardType {
			uid = info.UID
		}
		a.appEnabled.observe(base, uid, info.Version, &info.UptimeSeconds)
	}
	caps, err := cl.Capabilities(ctx)
	if err != nil {
		a.capsFetchFailed(gen, base, err)
		return
	}
	if !a.caps.put(gen, base, &caps) {
		a.logger.Info("device capabilities dropped: superseded by a newer fetch or a clock change", "base_url", base)
		return
	}
	a.logger.Info("device capabilities cached",
		"effects", len(caps.Effects), "palette_effects", len(caps.PaletteEffects),
		"transitions", len(caps.Transitions), "overlays", len(caps.Overlays),
		"palettes", len(caps.Palettes), "buzzer", caps.Audio.PlaysRTTTL(),
		"sound_object", caps.Audio.SoundObjectAPI(),
		"firmware", a.deviceFirmware())
}

func (a *App) capsFetchFailed(gen uint64, base string, err error) {
	stored, first := a.caps.fail(gen, base)
	switch {
	case !stored:
		a.logger.Debug("device capabilities fetch failed after a newer fetch or a clock change; ignored", "base_url", base, "err", err)
	case first:
		a.logger.Warn("device capabilities fetch failed", "base_url", base, "err", err)
	default:
		a.logger.Debug("device capabilities fetch failed", "base_url", base, "err", err)
	}
}

func (a *App) capabilities() (awtrix.Capabilities, bool) {
	return a.caps.get(a.cfg.Load().effectiveClockURL())
}

func (a *App) soundObjectAPI() bool {
	caps, ok := a.capabilities()
	return ok && caps.Audio.SoundObjectAPI()
}

func (a *App) deviceFirmware() string {
	v, _ := a.deviceVersion.Load().(string)
	return v
}

func (a *App) handleDeviceCapabilities(w http.ResponseWriter, r *http.Request) {
	if caps, ok := a.capabilities(); ok {
		writeJSON(w, http.StatusOK, caps)
		return
	}
	gen := a.caps.begin()
	base := a.cfg.Load().effectiveClockURL()
	body, err := a.clock.fetch(r.Context(), (*awtrix.Client).RawCapabilities)
	if err != nil {
		writeClockError(w, err)
		return
	}
	var caps awtrix.Capabilities
	if err := json.Unmarshal(body, &caps); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock capabilities not JSON: %w", err))
		return
	}
	a.caps.put(gen, base, &caps)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
