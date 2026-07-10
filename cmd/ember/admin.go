package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
)

// versionInfo is the JSON body served by /version. Computed once at startup.
type versionInfo struct {
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
}

func computeVersionInfo() versionInfo {
	info := versionInfo{Binary: "ember", Version: version, GoVersion: runtime.Version()}
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
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
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
// and newCfg. It walks Config's fields via reflection (unbounded recursion
// into nested config structs, see diffStructFields), deriving each path from
// the field's json tag — there is no hand-rolled leaf list to go stale, so a
// newly added Config field is diffed automatically the moment it exists,
// with no code change required here.
func diffConfig(oldCfg, newCfg Config) []string {
	var changed []string
	diffStructFields(reflect.ValueOf(oldCfg), reflect.ValueOf(newCfg), "", &changed)
	return changed
}

// diffStructFields appends prefix-qualified json-tag paths for every field of
// oldV/newV (both must be the same struct type) whose values differ.
// Struct-typed fields recurse unconditionally, however deep the nesting goes
// (today Config nests exactly one struct level, but a deeper future section
// is handled without changes here); every non-struct field is compared with
// reflect.DeepEqual, which correctly distinguishes nil vs. non-nil pointers
// (the pattern used throughout Config for "unset vs. explicit false/zero"
// optional fields).
func diffStructFields(oldV, newV reflect.Value, prefix string, changed *[]string) {
	t := oldV.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		ofv, nfv := oldV.Field(i), newV.Field(i)
		if ofv.Kind() == reflect.Struct {
			diffStructFields(ofv, nfv, path, changed)
			continue
		}
		if !reflect.DeepEqual(ofv.Interface(), nfv.Interface()) {
			*changed = append(*changed, path)
		}
	}
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
		// Same baseline repair loadConfig applies at startup: drop/replace
		// values that fail the SSRF-guard validators (e.g. a hand-edited
		// weather.icon_ids path-traversal entry) instead of loading them
		// live — a hand-edited config.json shouldn't bypass the guard just
		// because it arrived via reload instead of startup.
		sanitizeConfigBaseline(&newCfg, app.logger)
		// Token isn't in the JSON file (env-only), so carry it over from
		// the running config to keep the diff honest.
		//
		// The load (oldCfg) through the store below is one critical section
		// under cfgMu: it must observe and replace the same config value a
		// concurrent settings PUT (via updateConfig) would, or one of the two
		// changes is silently lost.
		app.cfgMu.Lock()
		oldCfg := *app.cfg.Load()
		newCfg.Auth.StatusToken = oldCfg.Auth.StatusToken
		if err := validateConfig(newCfg); err != nil {
			app.cfgMu.Unlock()
			logOutcome(http.StatusUnprocessableEntity, 0, err.Error())
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		changed := diffConfig(oldCfg, newCfg)
		if hit := nonReloadableChange(changed); hit != "" {
			app.cfgMu.Unlock()
			oldVal := formatLeafValue(oldCfg, hit)
			newVal := formatLeafValue(newCfg, hit)
			logOutcome(http.StatusConflict, len(changed), hit)
			writeError(w, http.StatusConflict, fmt.Errorf("non-reloadable field changed: %s=%s→%s (restart required)", hit, oldVal, newVal))
			return
		}
		app.cfg.Store(&newCfg)
		app.cfgMu.Unlock()
		// Keep the Pomodoro engine in sync with the reloaded config and
		// re-apply API-persisted settings so a reload doesn't revert them.
		app.resyncPomodoroAfterReload()
		// Likewise re-apply menu-persisted weather settings over the
		// freshly reloaded file config.
		app.loadPersistedWeatherSettings()
		// And meetings settings (same pattern: menu edits must survive a reload).
		app.loadPersistedMeetingsSettings()
		// And the menu-chosen clock URL (Device tab), so a reload doesn't drop
		// the store override back to the file-config baseline.
		app.loadPersistedDeviceBaseURL()
		app.loadPersistedUsageSettings()
		// Likewise re-apply display config overrides so a reload doesn't revert them.
		app.loadPersistedDisplaySettings()
		app.loadPersistedQuietSettings()
		logOutcome(http.StatusOK, len(changed), "")
		writeJSON(w, http.StatusOK, map[string]any{
			"reloaded":       true,
			"changed_fields": changed,
		})
	}
}
