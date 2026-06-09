package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

// deviceBaseURLKey is the writable-store key holding a menu-chosen clock URL.
// It overrides the read-only config.json baseline (mirrors the weather/Pomodoro
// config-persistence pattern).
const deviceBaseURLKey = "device_base_url"

// applyDeviceBaseURL validates a clock base URL, swaps it into the live config,
// and persists it to the store. Mirrors applyWeatherSettings.
func (a *App) applyDeviceBaseURL(raw string) error {
	if _, err := url.ParseRequestURI(raw); err != nil {
		return fmt.Errorf("invalid base_url %q", raw)
	}
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = raw
	a.cfg.Store(&cur)
	if a.store != nil {
		if err := a.store.PutSetting(deviceBaseURLKey, raw); err != nil {
			a.logger.Warn("device base url persist failed", "err", err)
		}
	}
	return nil
}

// loadPersistedDeviceBaseURL applies a previously menu-chosen clock URL.
func (a *App) loadPersistedDeviceBaseURL() {
	if a.store == nil {
		return
	}
	if v, ok, err := a.store.GetSetting(deviceBaseURLKey); err == nil && ok && v != "" {
		_ = a.applyDeviceBaseURL(v)
	}
}

// deviceSource reports where the effective clock URL came from:
// "store" (menu override) > "config" (config.json baseline) > "discovered"
// (mDNS auto-pick) > "none".
func (a *App) deviceSource() string {
	if a.store != nil {
		if _, ok, _ := a.store.GetSetting(deviceBaseURLKey); ok {
			return "store"
		}
	}
	cur := a.cfg.Load().AWTRIX.HTTPBaseURL
	switch {
	case cur != "" && cur == a.deviceBaseline:
		return "config"
	case cur != "":
		return "discovered"
	default:
		return "none"
	}
}

// initDeviceDiscovery runs once at boot. Resolution precedence:
//  1. writable-store override (menu choice) — wins, return.
//  2. config.json baseline, if it's reachable right now — keep.
//  3. mDNS auto-discovery — set in-memory only (never writes read-only config).
//
// The browse is bounded so it can't stall startup for long.
func (a *App) initDeviceDiscovery(ctx context.Context) {
	a.loadPersistedDeviceBaseURL()
	if a.deviceSource() == "store" {
		return
	}
	cl := &http.Client{Timeout: 1500 * time.Millisecond}
	if base := a.cfg.Load().AWTRIX.HTTPBaseURL; base != "" {
		if _, ok := discovery.Reachable(ctx, cl, base); ok {
			return
		}
	}
	cands, err := a.browseFn(ctx, 3*time.Second)
	if err != nil || len(cands) == 0 {
		a.logger.Info("clock discovery found no device", "configured", a.cfg.Load().AWTRIX.HTTPBaseURL)
		return
	}
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = cands[0].BaseURL
	a.cfg.Store(&cur) // in-memory only; not persisted
	a.logger.Info("clock auto-discovered", "base_url", cands[0].BaseURL, "uid", cands[0].UID)
}

func (a *App) handleDeviceConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"base_url": a.cfg.Load().AWTRIX.HTTPBaseURL,
		"source":   a.deviceSource(),
	})
}

func (a *App) handleDeviceConfigPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
	}
	if err := decodeJSON(w, r, &body, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.applyDeviceBaseURL(body.BaseURL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.handleDeviceConfigGet(w, r)
}

func (a *App) handleDeviceDiscover(w http.ResponseWriter, r *http.Request) {
	browse := a.browseFn
	if browse == nil {
		browse = discovery.BrowseAWTRIX
	}
	cands, err := browse(r.Context(), 3*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if cands == nil {
		cands = []discovery.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": cands,
		"effective":  a.cfg.Load().AWTRIX.HTTPBaseURL,
		"source":     a.deviceSource(),
	})
}
