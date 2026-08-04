package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

// capabilitiesTimeout bounds the startup/rediscovery capabilities fetch. Same
// budget as the reachability probe: a dark clock must not delay boot.
const capabilitiesTimeout = 2 * time.Second

// refreshCapabilities caches the clock's supported name lists (effects,
// transitions, overlays, palettes, radio, gpio) plus its firmware version.
// Called at startup and whenever rediscovery swaps to a different clock — the
// lists are per firmware build, so they only change when the device does.
// Failures leave the previous cache in place and log a warning; the endpoint
// below falls back to a live fetch, so a dark clock at boot is not fatal.
func (a *App) refreshCapabilities(ctx context.Context) {
	base := a.cfg.Load().AWTRIX.HTTPBaseURL
	if base == "" {
		return
	}
	cl := awtrix.NewClient(base, capabilitiesTimeout)
	if info, err := cl.DeviceInfo(ctx); err == nil {
		a.deviceVersion.Store(info.Version)
	}
	caps, err := cl.Capabilities(ctx)
	if err != nil {
		a.logger.Warn("device capabilities fetch failed", "base_url", base, "err", err)
		return
	}
	a.caps.Store(&caps)
	a.logger.Info("device capabilities cached",
		"effects", len(caps.Effects), "palette_effects", len(caps.PaletteEffects),
		"transitions", len(caps.Transitions), "overlays", len(caps.Overlays),
		"palettes", len(caps.Palettes), "radio", caps.Radio,
		"firmware", a.deviceFirmware())
}

// capabilities returns the cached capabilities, false when nothing is cached yet.
func (a *App) capabilities() (awtrix.Capabilities, bool) {
	if c := a.caps.Load(); c != nil {
		return *c, true
	}
	return awtrix.Capabilities{}, false
}

// deviceFirmware returns the clock's firmware version as last seen, "" if unknown.
func (a *App) deviceFirmware() string {
	v, _ := a.deviceVersion.Load().(string)
	return v
}

// handleDeviceCapabilities serves GET /v1/device/capabilities: the cached
// awtrix-ng capabilities document, verbatim in shape, so the menu app can render
// real effect/transition/overlay/palette pickers instead of a hardcoded table.
// An empty cache (clock was dark at startup) falls through to a live proxy fetch,
// which also warms the cache.
func (a *App) handleDeviceCapabilities(w http.ResponseWriter, r *http.Request) {
	if caps, ok := a.capabilities(); ok {
		writeJSON(w, http.StatusOK, caps)
		return
	}
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/capabilities", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
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
