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

	mux.HandleFunc("GET /v1/pomodoro/state", a.handlePomodoroState)
	mux.HandleFunc("GET /v1/pomodoro/stats", a.handlePomodoroStats)
	mux.HandleFunc("GET /v1/pomodoro/heatmap", a.handlePomodoroHeatmap)
	mux.HandleFunc("GET /v1/pomodoro/workhours", a.handlePomodoroWorkHours)
	mux.HandleFunc("GET /v1/pomodoro/dashboard", a.handlePomodoroDashboard)
	mux.HandleFunc("GET /v1/preview", a.handlePreview)
	mux.HandleFunc("GET /v1/weather/preview", a.handleWeatherPreview)
	mux.HandleFunc("GET /v1/pomodoro/preview", a.handlePomodoroPreview)
	mux.HandleFunc("GET /v1/reminders/preview", a.handleReminderPreview)
	mux.HandleFunc("GET /v1/meetings/preview", a.handleMeetingsPreview)
	mux.HandleFunc("GET /v1/meetings/state", a.handleMeetingsState)
	mux.HandleFunc("GET /v1/usage", a.handleUsageSnapshot)
	mux.Handle("GET /v1/activity/summary", rateLimit(a, http.HandlerFunc(a.handleActivitySummary)))
	mux.HandleFunc("GET /v1/weather/state", a.handleWeatherState)
	mux.Handle("GET /v1/clock/health", rateLimit(a, http.HandlerFunc(a.handleClockHealth)))
	mux.Handle("POST /hooks/awtrix/button", rateLimit(a, http.HandlerFunc(a.handleAwtrixButton)))
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
	mux.Handle("/v1/", rateLimit(a, requireAuth(a, a.logger, writeMux)))

	adminMux := http.NewServeMux()
	adminMux.Handle("GET /admin/doctor", handleAdminDoctor(a))
	adminMux.Handle("POST /admin/reload", handleAdminReload(a))
	mux.Handle("/admin/", rateLimit(a, adminRequireAuth(a, a.logger, adminMux)))

	return loggingMiddleware(a.logger, observeRequests(a, mux))
}

func (a *App) decodeOrReject(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if err := decodeJSON(w, r, dst, strict); err != nil {
		a.rejectBody(w, r, err)
		return false
	}
	return true
}

func (a *App) decodeOptionalOrReject(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if err := decodeJSON(w, r, dst, strict); err != nil && !errors.Is(err, io.EOF) {
		a.rejectBody(w, r, err)
		return false
	}
	return true
}

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

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, strict bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return err
		}
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
