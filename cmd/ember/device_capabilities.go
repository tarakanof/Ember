package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tarakanof/ember/internal/awtrix"
)

func (a *App) refreshCapabilities(ctx context.Context) {
	base := a.cfg.Load().effectiveClockURL()
	cl, err := a.clock.client(callCapabilities)
	if errors.Is(err, errClockNotConfigured) || errors.Is(err, errClockDisabled) {
		return
	}
	if err != nil {
		a.caps.Store(nil)
		a.logger.Warn("device capabilities fetch failed", "base_url", base, "err", err)
		return
	}
	if info, err := cl.DeviceInfo(ctx); err == nil {
		a.deviceVersion.Store(info.Version)
	}
	caps, err := cl.Capabilities(ctx)
	if err != nil {
		a.caps.Store(nil)
		a.logger.Warn("device capabilities fetch failed", "base_url", base, "err", err)
		return
	}
	a.caps.Store(&caps)
	a.logger.Info("device capabilities cached",
		"effects", len(caps.Effects), "palette_effects", len(caps.PaletteEffects),
		"transitions", len(caps.Transitions), "overlays", len(caps.Overlays),
		"palettes", len(caps.Palettes), "buzzer", caps.Audio.Buzzer,
		"firmware", a.deviceFirmware())
}

func (a *App) capabilities() (awtrix.Capabilities, bool) {
	if c := a.caps.Load(); c != nil {
		return *c, true
	}
	return awtrix.Capabilities{}, false
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
	a.caps.Store(&caps)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
