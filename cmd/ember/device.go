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

// defaultDeviceBaseURL is the fallback clock URL used both when config.json
// omits awtrix.http_base_url (see Config.applyDefaults) and when a
// hand-edited baseline fails validDeviceURL (see sanitizeConfigBaseline).
const defaultDeviceBaseURL = "http://192.168.0.14"

// validDeviceURL reports (via a non-nil error) whether raw is unsafe to use as
// the clock's base URL. The /v1/device/* proxies forward requests to this URL
// verbatim, so it must be an absolute http/https URL with a non-empty host —
// otherwise a file:, gopher:, or bare-path value could be used for SSRF or to
// read local files. Applied to both the PUT /v1/device/config body and the
// config.json baseline (see sanitizeConfigBaseline).
func validDeviceURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url %q: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url %q: scheme must be http or https, got %q", raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("base_url %q: host is required", raw)
	}
	return nil
}

// applyDeviceBaseURL validates a clock base URL, swaps it into the live config,
// and persists it to the store. Mirrors applyWeatherSettings.
func (a *App) applyDeviceBaseURL(raw string) error {
	if err := validDeviceURL(raw); err != nil {
		return err
	}
	a.updateConfig(func(cur *Config) { cur.AWTRIX.HTTPBaseURL = raw })
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
	case cur == "":
		return "none"
	case a.deviceAutoPicked:
		// Discovery set this URL at boot — even if it happens to equal the
		// (unreachable) config.json baseline, it was reached via discovery.
		return "discovered"
	case cur == a.deviceBaseline:
		return "config"
	default:
		return "discovered"
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
	base := cands[0].BaseURL
	a.updateConfig(func(cur *Config) { cur.AWTRIX.HTTPBaseURL = base }) // in-memory only; not persisted
	a.deviceAutoPicked = true
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
