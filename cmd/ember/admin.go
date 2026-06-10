package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
)

// versionInfo is the JSON body served by /version. Computed once at startup.
type versionInfo struct {
	Binary    string `json:"binary"`
	Revision  string `json:"revision"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
}

func computeVersionInfo() versionInfo {
	info := versionInfo{Binary: "ember", GoVersion: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Revision = s.Value
			case "vcs.modified":
				info.Dirty = s.Value == "true"
			}
		}
	}
	return info
}

func handleVersion(info versionInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}
}

// adminRequireAuth is stricter than requireAuth: empty token = closed door.
// Admin endpoints expose mutation (reload) and runtime detail (sessions),
// neither of which should be open by default just because the operator
// hasn't set EMBER_TOKEN yet.
func adminRequireAuth(app *App, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := app.cfg.Load().Auth.StatusToken
		if token == "" {
			logger.InfoContext(r.Context(), "admin disabled",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			writeError(w, http.StatusUnauthorized, errors.New("admin disabled: EMBER_TOKEN unset"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			logger.InfoContext(r.Context(), "admin auth rejected",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleAdminDoctor(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := app.cfg.Load()
		res := runDoctorChecks(r.Context(), app, cfg)
		status := http.StatusOK
		if !res.OK {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(res)
	}
}

// nonReloadableLeaves are config paths that cannot change at runtime: the
// HTTP listener is bound once at startup, and admin auth tokens / refresh
// cadence are wired into long-lived structures. Any change to these triggers
// 409 Conflict from /admin/reload — operator must restart the process.
var nonReloadableLeaves = []string{
	"http.addr",
	"auth.status_token",
	"auth.status_token_env",
	"display.refresh_seconds",
}

// diffConfig returns dotted leaf paths whose values differ between oldCfg
// and newCfg. Hand-rolled (no reflection) across all 18 config leaves so a
// new field added later forces a compile-time prompt to extend this list.
func diffConfig(oldCfg, newCfg Config) []string {
	var changed []string
	if oldCfg.HTTP.Addr != newCfg.HTTP.Addr {
		changed = append(changed, "http.addr")
	}
	if oldCfg.AWTRIX.HTTPBaseURL != newCfg.AWTRIX.HTTPBaseURL {
		changed = append(changed, "awtrix.http_base_url")
	}
	if oldCfg.AWTRIX.AppName != newCfg.AWTRIX.AppName {
		changed = append(changed, "awtrix.app_name")
	}
	if oldCfg.AWTRIX.TimeoutSeconds != newCfg.AWTRIX.TimeoutSeconds {
		changed = append(changed, "awtrix.timeout_seconds")
	}
	if oldCfg.Auth.StatusToken != newCfg.Auth.StatusToken {
		changed = append(changed, "auth.status_token")
	}
	if oldCfg.Auth.StatusTokenEnv != newCfg.Auth.StatusTokenEnv {
		changed = append(changed, "auth.status_token_env")
	}
	if oldCfg.Display.IdleText != newCfg.Display.IdleText {
		changed = append(changed, "display.idle_text")
	}
	if oldCfg.Display.StaleSeconds != newCfg.Display.StaleSeconds {
		changed = append(changed, "display.stale_seconds")
	}
	if oldCfg.Display.DoneTTLSeconds != newCfg.Display.DoneTTLSeconds {
		changed = append(changed, "display.done_ttl_seconds")
	}
	if oldCfg.Display.HeartbeatSeconds != newCfg.Display.HeartbeatSeconds {
		changed = append(changed, "display.heartbeat_seconds")
	}
	if oldCfg.Display.RefreshSeconds != newCfg.Display.RefreshSeconds {
		changed = append(changed, "display.refresh_seconds")
	}
	if oldCfg.Display.NotifyOnWaiting != newCfg.Display.NotifyOnWaiting {
		changed = append(changed, "display.notify_on_waiting")
	}
	if oldCfg.Display.FrameLifetimeSeconds != newCfg.Display.FrameLifetimeSeconds {
		changed = append(changed, "display.frame_lifetime_seconds")
	}
	if oldCfg.Display.IdleRestoreSeconds != newCfg.Display.IdleRestoreSeconds {
		changed = append(changed, "display.idle_restore_seconds")
	}
	if oldCfg.RateLimit.Disabled != newCfg.RateLimit.Disabled {
		changed = append(changed, "rate_limit.disabled")
	}
	if oldCfg.RateLimit.Burst != newCfg.RateLimit.Burst {
		changed = append(changed, "rate_limit.burst")
	}
	if oldCfg.RateLimit.RefillPerSec != newCfg.RateLimit.RefillPerSec {
		changed = append(changed, "rate_limit.refill_per_sec")
	}
	if oldCfg.RateLimit.IdleEvictSeconds != newCfg.RateLimit.IdleEvictSeconds {
		changed = append(changed, "rate_limit.idle_evict_seconds")
	}
	return changed
}

// nonReloadableChange returns the first changed leaf path that appears in
// nonReloadableLeaves, or "" if all changes are safe to apply at runtime.
func nonReloadableChange(changed []string) string {
	for _, c := range changed {
		for _, n := range nonReloadableLeaves {
			if c == n {
				return c
			}
		}
	}
	return ""
}

// formatLeafValue renders a single config leaf as a string for inclusion in
// the 409 error message. auth.status_token is redacted to avoid leaking the
// running secret in an error response — defense-in-depth even though the
// preserve-token copy normally prevents that leaf from diffing.
func formatLeafValue(cfg Config, leaf string) string {
	switch leaf {
	case "http.addr":
		return cfg.HTTP.Addr
	case "auth.status_token":
		return "<redacted>"
	case "auth.status_token_env":
		return cfg.Auth.StatusTokenEnv
	case "display.refresh_seconds":
		return fmt.Sprintf("%d", cfg.Display.RefreshSeconds)
	}
	return ""
}

// handleAdminReload re-reads the config file path captured at startup,
// validates, and atomically swaps via app.cfg.Store. State machine:
//   - 412 if server started from defaults (no file path).
//   - 500 on read error.
//   - 400 on parse error.
//   - 422 on validation error.
//   - 409 if any non-reloadable leaf changed.
//   - 200 with {reloaded, changed_fields} on success.
//
// Auth.StatusToken is preserved from the running config because the JSON
// file doesn't carry it (env-only by repo policy); without this copy a
// reload would always trip the 409 guard for auth.status_token.
func handleAdminReload(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			app.logger.InfoContext(r.Context(), "request rejected",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"reason", "too_large",
			)
			writeError(w, http.StatusRequestEntityTooLarge, err)
			return
		}

		logOutcome := func(status int, changed int, detail string) {
			app.logger.InfoContext(r.Context(), "admin reload",
				"status", status,
				"changed_fields_count", changed,
				"detail", detail,
			)
		}

		if app.configSource == "defaults" {
			logOutcome(http.StatusPreconditionFailed, 0, "no config source")
			writeError(w, http.StatusPreconditionFailed, errors.New("no config source: server started from defaults; reload requires a config file"))
			return
		}
		newCfg, err := parseConfigFile(app.configPath)
		if err != nil {
			switch {
			case errors.Is(err, ErrConfigRead):
				logOutcome(http.StatusInternalServerError, 0, err.Error())
				writeError(w, http.StatusInternalServerError, err)
			case errors.Is(err, ErrConfigParse):
				logOutcome(http.StatusBadRequest, 0, err.Error())
				writeError(w, http.StatusBadRequest, err)
			default:
				logOutcome(http.StatusInternalServerError, 0, err.Error())
				writeError(w, http.StatusInternalServerError, err)
			}
			return
		}
		// Required-field check on the RAW parsed config: applyDefaults
		// would mask an explicit operator empty string by filling in the
		// fallback URL, hiding their misconfiguration.
		if newCfg.AWTRIX.HTTPBaseURL == "" {
			err := fmt.Errorf("%w: awtrix.http_base_url is required", ErrConfigValidate)
			logOutcome(http.StatusUnprocessableEntity, 0, err.Error())
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		newCfg.applyDefaults()
		// Token isn't in the JSON file (env-only), so carry it over from
		// the running config to keep the diff honest.
		oldCfg := *app.cfg.Load()
		newCfg.Auth.StatusToken = oldCfg.Auth.StatusToken
		if err := validateConfig(newCfg); err != nil {
			logOutcome(http.StatusUnprocessableEntity, 0, err.Error())
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		changed := diffConfig(oldCfg, newCfg)
		if hit := nonReloadableChange(changed); hit != "" {
			oldVal := formatLeafValue(oldCfg, hit)
			newVal := formatLeafValue(newCfg, hit)
			logOutcome(http.StatusConflict, len(changed), hit)
			writeError(w, http.StatusConflict, fmt.Errorf("non-reloadable field changed: %s=%s→%s (restart required)", hit, oldVal, newVal))
			return
		}
		app.cfg.Store(&newCfg)
		// Keep the Pomodoro engine in sync with the reloaded config and
		// re-apply API-persisted settings so a reload doesn't revert them.
		app.resyncPomodoroAfterReload()
		// Likewise re-apply menu-persisted weather settings over the
		// freshly reloaded file config.
		app.loadPersistedWeatherSettings()
		// And the menu-chosen clock URL (Device tab), so a reload doesn't drop
		// the store override back to the file-config baseline.
		app.loadPersistedDeviceBaseURL()
		logOutcome(http.StatusOK, len(changed), "")
		writeJSON(w, http.StatusOK, map[string]any{
			"reloaded":       true,
			"changed_fields": changed,
		})
	}
}
