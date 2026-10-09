package main

import (
	"context"
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

type versionInfo struct {
	Binary    string   `json:"binary"`
	Version   string   `json:"version"`
	Revision  string   `json:"revision"`
	Dirty     bool     `json:"dirty"`
	GoVersion string   `json:"go_version"`
	Features  []string `json:"features"`
}

const featureFirmwareDeleteKeep = "firmware_delete_keep"

func computeVersionInfo() versionInfo {
	info := versionInfo{Binary: "ember", Version: version, GoVersion: runtime.Version(),
		Features: []string{featureFirmwareDeleteKeep}}
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

var nonReloadableLeaves = []string{
	"http.addr",
	"auth.status_token",
	"auth.status_token_env",
}

func diffConfig(oldCfg, newCfg Config) []string {
	var changed []string
	diffStructFields(reflect.ValueOf(oldCfg), reflect.ValueOf(newCfg), "", &changed)
	return changed
}

func diffStructFields(oldV, newV reflect.Value, prefix string, changed *[]string) {
	t := oldV.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
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

func formatLeafValue(cfg Config, leaf string) string {
	switch leaf {
	case "http.addr":
		return cfg.HTTP.Addr
	case "auth.status_token":
		return "<redacted>"
	case "auth.status_token_env":
		return cfg.Auth.StatusTokenEnv
	}
	return ""
}

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
		if newCfg.AWTRIX.HTTPBaseURL == "" {
			err := fmt.Errorf("%w: awtrix.http_base_url is required", ErrConfigValidate)
			logOutcome(http.StatusUnprocessableEntity, 0, err.Error())
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		newCfg.applyDefaults()
		sanitizeConfigBaseline(&newCfg, app.logger)
		warnDeprecatedConfig(newCfg, app.logger)
		releaseRotation := app.holdClockRotation()
		defer releaseRotation()
		app.cfgMu.Lock()
		oldCfg := *app.cfg.Load()
		newCfg.Auth.StatusToken = oldCfg.Auth.StatusToken
		carryClockURL(oldCfg, &newCfg)
		carryClockPresentation(oldCfg, &newCfg)
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
		app.resyncPomodoroAfterReload()
		app.reapplySettings()
		app.changes.notify(topicConfig | topicPomodoro)
		if !clockDisabled() {
			go app.ensureBootPingScript(context.Background())
		}
		logOutcome(http.StatusOK, len(changed), "")
		writeJSON(w, http.StatusOK, map[string]any{
			"reloaded":       true,
			"changed_fields": changed,
		})
	}
}
