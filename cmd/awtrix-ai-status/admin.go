package main

import (
	"encoding/json"
	"errors"
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
	info := versionInfo{Binary: "awtrix-ai-status", GoVersion: runtime.Version()}
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
// hasn't set STATUS_TOKEN yet.
func adminRequireAuth(app *App, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := app.cfg.Load().Auth.StatusToken
		if token == "" {
			writeError(w, http.StatusUnauthorized, errors.New("admin disabled: STATUS_TOKEN unset"))
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
