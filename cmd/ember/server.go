package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func handleMetrics(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		app.metrics.render(w, app)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Snapshot())
	})
	mux.Handle("GET /version", handleVersion(a.versionInfo))
	mux.Handle("GET /metrics", handleMetrics(a))

	// Pomodoro reads are open like /state. The button hook is unauthenticated
	// because the AWTRIX device's button_callback cannot send a bearer token;
	// it only maps presses to timer actions, so LAN blast radius is minimal.
	mux.HandleFunc("GET /v1/pomodoro/state", a.handlePomodoroState)
	mux.HandleFunc("GET /v1/pomodoro/stats", a.handlePomodoroStats)
	mux.HandleFunc("GET /v1/pomodoro/heatmap", a.handlePomodoroHeatmap)
	mux.HandleFunc("GET /v1/pomodoro/workhours", a.handlePomodoroWorkHours)
	mux.HandleFunc("GET /v1/pomodoro/dashboard", a.handlePomodoroDashboard)
	// Open, read-only render preview for the menu app's Display tab. The
	// specific GET pattern wins over the "/v1/" requireAuth catch-all below.
	mux.HandleFunc("GET /v1/preview", a.handlePreview)
	mux.HandleFunc("GET /v1/weather/preview", a.handleWeatherPreview)
	mux.HandleFunc("GET /v1/pomodoro/preview", a.handlePomodoroPreview)
	mux.HandleFunc("GET /v1/reminders/preview", a.handleReminderPreview)
	mux.HandleFunc("GET /v1/meetings/preview", a.handleMeetingsPreview)
	mux.HandleFunc("GET /v1/meetings/state", a.handleMeetingsState)
	// Dashboard reads (dashboard_http.go). GET /v1/usage shares its path with
	// the authed POST, which still falls through to the /v1/ write mux.
	mux.HandleFunc("GET /v1/usage", a.handleUsageSnapshot)
	// The two that do I/O (a DB scan; a clock probe and release lookup) are
	// per-IP rate-limited like the device hooks.
	mux.Handle("GET /v1/activity/summary", rateLimit(a, http.HandlerFunc(a.handleActivitySummary)))
	mux.HandleFunc("GET /v1/weather/state", a.handleWeatherState)
	mux.Handle("GET /v1/clock/health", rateLimit(a, http.HandlerFunc(a.handleClockHealth)))
	// Unauthenticated (the device can't hold a token) but per-IP rate-limited.
	mux.Handle("POST /hooks/awtrix/button", rateLimit(a, http.HandlerFunc(a.handleAwtrixButton)))
	// Same trust model as the button hook, and the same reason: a Berry script
	// on the clock has nowhere to keep a token. See handleAwtrixBoot.
	mux.Handle("POST "+bootHookPath, rateLimit(a, http.HandlerFunc(a.handleAwtrixBoot)))

	writeMux := http.NewServeMux()
	writeMux.Handle("POST /v1/status", http.HandlerFunc(a.handleStatus))
	writeMux.Handle("DELETE /v1/status", http.HandlerFunc(a.handleDeleteStatus))
	writeMux.Handle("POST /v1/clear", http.HandlerFunc(a.handleClear))
	writeMux.Handle("POST /v1/notify", http.HandlerFunc(a.handleNotify))
	writeMux.Handle("POST /v1/pomodoro/start", http.HandlerFunc(a.handlePomodoroStart))
	writeMux.Handle("POST /v1/pomodoro/pause", http.HandlerFunc(a.handlePomodoroPause))
	writeMux.Handle("POST /v1/pomodoro/resume", http.HandlerFunc(a.handlePomodoroResume))
	writeMux.Handle("POST /v1/pomodoro/stop", http.HandlerFunc(a.handlePomodoroStop))
	writeMux.Handle("POST /v1/pomodoro/skip", http.HandlerFunc(a.handlePomodoroSkip))
	writeMux.Handle("GET /v1/pomodoro/config", http.HandlerFunc(a.handlePomodoroConfigGet))
	writeMux.Handle("PUT /v1/pomodoro/config", http.HandlerFunc(a.handlePomodoroConfigPut))
	writeMux.Handle("GET /v1/apps", http.HandlerFunc(a.handleAppsGet))
	writeMux.Handle("PUT /v1/apps", http.HandlerFunc(a.handleAppsPut))
	writeMux.Handle("POST /v1/usage", http.HandlerFunc(a.handleUsage))
	writeMux.Handle("GET /v1/usage/config", http.HandlerFunc(a.handleUsageConfigGet))
	writeMux.Handle("PUT /v1/usage/config", http.HandlerFunc(a.handleUsageConfigPut))
	writeMux.Handle("GET /v1/display/config", http.HandlerFunc(a.handleDisplayConfigGet))
	writeMux.Handle("PUT /v1/display/config", http.HandlerFunc(a.handleDisplayConfigPut))
	writeMux.Handle("GET /v1/quiet/config", http.HandlerFunc(a.handleQuietConfigGet))
	writeMux.Handle("PUT /v1/quiet/config", http.HandlerFunc(a.handleQuietConfigPut))
	writeMux.Handle("GET /v1/weather/config", http.HandlerFunc(a.handleWeatherConfigGet))
	writeMux.Handle("PUT /v1/weather/config", http.HandlerFunc(a.handleWeatherConfigPut))
	writeMux.Handle("GET /v1/meetings/config", http.HandlerFunc(a.handleMeetingsConfigGet))
	writeMux.Handle("PUT /v1/meetings/config", http.HandlerFunc(a.handleMeetingsConfigPut))
	writeMux.Handle("POST /v1/reminders/fire", http.HandlerFunc(a.handleReminderFire))
	writeMux.Handle("GET /v1/device/discover", http.HandlerFunc(a.handleDeviceDiscover))
	writeMux.Handle("GET /v1/device/config", http.HandlerFunc(a.handleDeviceConfigGet))
	writeMux.Handle("PUT /v1/device/config", http.HandlerFunc(a.handleDeviceConfigPut))
	writeMux.Handle("GET /v1/device/settings", http.HandlerFunc(a.handleDeviceSettingsGet))
	writeMux.Handle("PUT /v1/device/settings", http.HandlerFunc(a.handleDeviceSettingsPut))
	writeMux.Handle("GET /v1/device/display", http.HandlerFunc(a.handleDeviceDisplayGet))
	writeMux.Handle("PUT /v1/device/display", http.HandlerFunc(a.handleDeviceDisplayPut))
	writeMux.Handle("PUT /v1/device/display/power", http.HandlerFunc(a.handleDevicePowerPut))
	writeMux.Handle("POST /v1/device/audio/test", http.HandlerFunc(a.handleDeviceAudioTest))
	writeMux.Handle("POST /v1/device/audio/stop", http.HandlerFunc(a.handleDeviceAudioStop))
	writeMux.Handle("GET /v1/device/audio/melodies", http.HandlerFunc(a.handleDeviceAudioMelodies))
	writeMux.Handle("GET /v1/device/apps", http.HandlerFunc(a.handleDeviceAppsGet))
	writeMux.Handle("PUT /v1/device/apps", http.HandlerFunc(a.handleDeviceAppsPut))
	writeMux.Handle("GET /v1/device/stats", http.HandlerFunc(a.handleDeviceStats))
	writeMux.Handle("GET /v1/device/capabilities", http.HandlerFunc(a.handleDeviceCapabilities))
	writeMux.Handle("GET /v1/device/sensors", http.HandlerFunc(a.handleDeviceSensorsGet))
	writeMux.Handle("PUT /v1/device/sensors", http.HandlerFunc(a.handleDeviceSensorsPut))
	writeMux.Handle("GET /v1/device/screen", http.HandlerFunc(a.handleDeviceScreen))
	writeMux.Handle("POST /v1/device/reboot", http.HandlerFunc(a.handleDeviceReboot))
	writeMux.Handle("POST /v1/device/notify/dismiss", http.HandlerFunc(a.handleDeviceDismiss))
	writeMux.Handle("POST /v1/device/app/next", http.HandlerFunc(a.handleDeviceNextApp))
	writeMux.Handle("POST /v1/device/app/previous", http.HandlerFunc(a.handleDevicePrevApp))
	writeMux.Handle("GET /v1/device/buttons", http.HandlerFunc(a.handleDeviceButtons))
	writeMux.Handle("PUT /v1/device/buttons", http.HandlerFunc(a.handleDeviceButtonsPut))
	// Limiter outermost, auth inside: requests rejected by auth (401) still
	// consume rate-limit budget, so an attacker hammering wrong tokens gets
	// throttled to 429 instead of probing at full speed.
	mux.Handle("/v1/", rateLimit(a, requireAuth(a, a.logger, writeMux)))

	adminMux := http.NewServeMux()
	adminMux.Handle("GET /admin/doctor", handleAdminDoctor(a))
	adminMux.Handle("POST /admin/reload", handleAdminReload(a))
	// Same limiter-outside-auth ordering as /v1/: admin endpoints authenticate
	// with the same token, so their 401s must consume rate-limit budget too —
	// otherwise an attacker throttled on /v1/ could probe the token at full
	// speed via /admin/ 401s.
	mux.Handle("/admin/", rateLimit(a, adminRequireAuth(a, a.logger, adminMux)))

	// Order: logging outermost so the access log sees the original
	// response status; observeRequests inside so it can read the same.
	return loggingMiddleware(a.logger, observeRequests(a, mux))
}

// decodeOrReject decodes r's JSON body into dst (see decodeJSON) and, when
// that fails, answers the request itself via rejectBody. It reports whether
// the handler should go on.
func (a *App) decodeOrReject(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if err := decodeJSON(w, r, dst, strict); err != nil {
		a.rejectBody(w, r, err)
		return false
	}
	return true
}

// decodeOptionalOrReject is decodeOrReject for endpoints whose body is
// optional: an empty body leaves dst untouched and succeeds.
func (a *App) decodeOptionalOrReject(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if err := decodeJSON(w, r, dst, strict); err != nil && !errors.Is(err, io.EOF) {
		a.rejectBody(w, r, err)
		return false
	}
	return true
}

// rejectBody answers a request whose body could not be read or decoded: 413
// when it ran past the http.MaxBytesReader cap, 400 otherwise, with one
// "request rejected" line so the reason survives beyond the access log.
func (a *App) rejectBody(w http.ResponseWriter, r *http.Request, err error) {
	reason, status := "parse", http.StatusBadRequest
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		reason, status = "too_large", http.StatusRequestEntityTooLarge
	}
	a.logger.InfoContext(r.Context(), "request rejected",
		"remote_addr", r.RemoteAddr,
		"path", r.URL.Path,
		"reason", reason,
	)
	writeError(w, status, err)
}

// decodeJSON reads one JSON value from r's body, capped at 1 MB, and rejects
// trailing data. strict also rejects unknown fields. Handlers normally go
// through decodeOrReject, which maps the error to a response.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, strict bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Trailing-tokens detection: a second Decode must return io.EOF.
	// dec.More() (the prior implementation) only reports true for nested
	// continuations (mid-array/mid-object), not for trailing top-level
	// values like {...}{...}.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing tokens after JSON body")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// requireAuth wraps next with bearer-token auth. Reads the token from
// app.cfg.Load() per request so token rotation via container restart
// (or future /admin/reload) takes effect for the next request after the
// swap. Fails closed: an empty configured token rejects every write, so a
// misconfigured deploy never silently exposes /v1 writes to the LAN.
// (Admin endpoints use the identical policy via adminRequireAuth.)
func requireAuth(app *App, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := app.cfg.Load().Auth.StatusToken
		if token == "" {
			logger.InfoContext(r.Context(), "auth disabled",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"method", r.Method,
			)
			writeError(w, http.StatusUnauthorized, errors.New("writes disabled: EMBER_TOKEN unset"))
			return
		}
		expected := "Bearer " + token
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			logger.InfoContext(r.Context(), "auth rejected",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"method", r.Method,
			)
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware writes one access-log line per request. Successful
// requests log at Debug (STYLE §7): the menu polls several GETs every few
// seconds and producers heartbeat POST /v1/status every 2-10s, which at Info
// would bury the transitions the log exists for. Handlers log their own
// decisions (reload outcome, rejections). Any status >= 400 stays at Info.
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if rec.status < http.StatusBadRequest {
			level = slog.LevelDebug
		}
		logger.Log(r.Context(), level, "http request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start))
	})
}
