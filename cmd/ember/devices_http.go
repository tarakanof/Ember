package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type deviceIDKey struct{}

func deviceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(deviceIDKey{}).(string)
	return id
}

// requireDevice admits only a device bearer token.
func requireDevice(a *App, next http.Handler) http.Handler {
	return deviceAuth(a, nil, next)
}

// requireOwnerOrDevice admits EMBER_TOKEN or a device bearer token.
func requireOwnerOrDevice(a *App, next http.Handler) http.Handler {
	return deviceAuth(a, next, next)
}

// rateLimitAuthFailures is rateLimit for routes an authenticated knob
// calls in bursts (the view re-arm, control steps): a client whose IP has
// spent its bucket on failed tokens gets 429 before any token check, a
// failed token spends one, and a valid token spends none (the routes keep
// their own per-caller caps). Brute force stays as limited as elsewhere.
func rateLimitAuthFailures(a *App, owner, device http.Handler) http.Handler {
	return deviceAuthWith(a, owner, device, true)
}

// View requests per knob: a long-poll re-arm after each change, plain polls
// every poll_ms; this is far above either.
const (
	viewBurst  = 30
	viewPerSec = 10.0
)

// perDevice answers 429 when the authenticated device spent its bucket.
func (a *App) perDevice(l *callerLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allowNow(deviceIDFrom(r.Context())) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, errors.New("too many view requests"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func deviceAuth(a *App, owner, device http.Handler) http.Handler {
	return deviceAuthWith(a, owner, device, false)
}

func deviceAuthWith(a *App, owner, device http.Handler, chargeFailures bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if chargeFailures && !a.limiter.Has(clientIP(r)) {
			a.metrics.incRateLimitDenied()
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, errors.New("rate limit exceeded"))
			return
		}
		token := a.cfg.Load().Auth.StatusToken
		if token == "" {
			a.logger.InfoContext(r.Context(), "auth disabled",
				"remote_addr", r.RemoteAddr, "path", r.URL.Path, "method", r.Method)
			writeError(w, http.StatusUnauthorized, errors.New("writes disabled: EMBER_TOKEN unset"))
			return
		}
		header := r.Header.Get("Authorization")
		if owner != nil && subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) == 1 {
			owner.ServeHTTP(w, r)
			return
		}
		bearer, _ := strings.CutPrefix(header, "Bearer ")
		id, ok, err := a.devices.authenticate(bearer)
		if err != nil {
			a.logger.WarnContext(r.Context(), "device auth failed", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, errors.New("device registry unavailable"))
			return
		}
		if !ok {
			if chargeFailures {
				a.limiter.Allow(clientIP(r))
			}
			a.logger.InfoContext(r.Context(), "auth rejected",
				"remote_addr", r.RemoteAddr, "path", r.URL.Path, "method", r.Method)
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		device.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceIDKey{}, id)))
	})
}

func (a *App) writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errDeviceNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, errSettingBody), errors.Is(err, errDeviceBody):
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path, "reason", "invalid")
		writeError(w, http.StatusBadRequest, err)
	default:
		a.logger.WarnContext(r.Context(), "device registry write failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("device registry write failed"))
	}
}

type mintedDevice struct {
	deviceView
	Token string `json:"token"`
}

func (a *App) handleDevicesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		HwID string `json:"hw_id"`
		Name string `json:"name"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if req.Kind != deviceKindKnob {
		a.writeDeviceError(w, r, fmt.Errorf("%w: kind must be %q", errDeviceBody, deviceKindKnob))
		return
	}
	hwID, err := normalizeHwID(req.HwID)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	name, err := normalizeDeviceName(req.Name, false)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	view, token, created, err := a.devices.provision(hwID, name)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device provisioned", "device_id", view.ID, "kind", view.Kind, "reprovisioned", !created)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, mintedDevice{deviceView: view, Token: token})
}

func (a *App) handleDevicesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"devices": a.devices.list()})
}

func (a *App) handleDeviceConfigGetOwner(w http.ResponseWriter, r *http.Request) {
	cfg, version, err := a.devices.config(r.PathValue("id"))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleDeviceConfigPutOwner(w http.ResponseWriter, r *http.Request) {
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		return
	}
	id := r.PathValue("id")
	cfg, version, changed, err := a.devices.putConfig(id, patch)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if changed {
		a.logger.InfoContext(r.Context(), "device config updated", "device_id", id, "config_version", version)
	}
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleDevicePatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	name, err := normalizeDeviceName(req.Name, true)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	view, err := a.devices.rename(r.PathValue("id"), name)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *App) handleDeviceRotate(w http.ResponseWriter, r *http.Request) {
	view, err := a.devices.rotate(r.PathValue("id"))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device token rotation started", "device_id", view.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func (a *App) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.devices.remove(id); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.knobStats.forget(id)
	a.wifiDrops.forget(id)
	a.logger.InfoContext(r.Context(), "device deleted", "device_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleDeviceCheckin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FW                  string          `json:"fw"`
		IP                  string          `json:"ip"`
		RSSI                int             `json:"rssi"`
		HeapInternalFree    int             `json:"heap_internal_free"`
		HeapInternalLargest int             `json:"heap_internal_largest"`
		UptimeS             int64           `json:"uptime_s"`
		ConfigVersion       int             `json:"config_version"`
		LinkMHz             int             `json:"link_mhz"`
		LinkFallback        bool            `json:"link_fallback"`
		Wifi                json.RawMessage `json:"wifi"`
		Stats               json.RawMessage `json:"stats"`
	}
	if !a.decodeOptionalOrReject(w, r, &req, false) {
		return
	}
	if utf8.RuneCountInString(req.FW) > 32 || !utf8.ValidString(req.FW) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: fw must be at most 32 characters", errDeviceBody))
		return
	}
	if req.LinkMHz < 0 || req.LinkMHz > 1000 {
		a.writeDeviceError(w, r, fmt.Errorf("%w: link_mhz must be 0..1000", errDeviceBody))
		return
	}
	ip := clientIP(r)
	if req.IP != "" {
		addr, err := netip.ParseAddr(req.IP)
		if err != nil {
			a.writeDeviceError(w, r, fmt.Errorf("%w: ip must be an IP address", errDeviceBody))
			return
		}
		ip = addr.String()
	}
	id := deviceIDFrom(r.Context())
	stats := a.decodeKnobStats(r, req.Stats)
	report := deviceCheckin{
		FW:                  req.FW,
		IP:                  ip,
		RSSI:                req.RSSI,
		HeapInternalFree:    req.HeapInternalFree,
		HeapInternalLargest: req.HeapInternalLargest,
		UptimeS:             req.UptimeS,
		AppliedVersion:      req.ConfigVersion,
		LinkMHz:             req.LinkMHz,
		LinkFallback:        req.LinkFallback && req.LinkMHz > 0,
		Wifi:                a.decodeDeviceWifi(r, req.Wifi),
	}
	res, err := a.devices.checkin(id, report)
	if errors.Is(err, errCheckinNotStored) {
		a.logger.WarnContext(r.Context(), "device checkin not persisted", "device_id", id, "err", err)
		err = nil
	}
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	now := a.knobStats.now()
	if diag, _, err := a.devices.diagnostics(id); err == nil {
		if stats = stats.forLevel(diag); stats != nil {
			sample := knobSampleFromReport(report, stats)
			sample.BrightnessLevel = refOf(a.currentBrightness(now).Level)
			a.knobStats.record(id, now, sample)
		}
		res.DiagLiveUntil = a.knobLiveUnix(id, diag, now)
	}
	if res.NewToken != "" {
		a.logger.InfoContext(r.Context(), "device rotation token issued", "device_id", id)
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set(knobNowHeader, unixHeader(now))
	writeJSON(w, http.StatusOK, res)
}

func (a *App) decodeDeviceWifi(r *http.Request, raw json.RawMessage) *deviceWifi {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var w deviceWifi
	err := json.Unmarshal(raw, &w)
	if err == nil {
		err = w.validate()
	}
	id := deviceIDFrom(r.Context())
	if err != nil {
		level := slog.LevelDebug
		if a.wifiDrops.changed(id, err.Error()) {
			level = slog.LevelInfo
		}
		a.logger.Log(r.Context(), level, "device wifi dropped", "device_id", id, "err", err)
		return nil
	}
	a.wifiDrops.changed(id, "")
	return &w
}

// wifiDropLog keeps a knob that keeps sending the same bad wifi object from
// logging it at Info on every checkin.
type wifiDropLog struct {
	mu   sync.Mutex        // protects last
	last map[string]string // device ID -> last drop reason
}

// changed records reason ("" for a valid object) and reports whether it
// differs from the device's previous one.
func (l *wifiDropLog) changed(id, reason string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last[id] == reason {
		return false
	}
	if l.last == nil {
		l.last = make(map[string]string)
	}
	if reason == "" {
		delete(l.last, id)
	} else {
		l.last[id] = reason
	}
	return true
}

func (l *wifiDropLog) forget(id string) {
	l.mu.Lock()
	delete(l.last, id)
	l.mu.Unlock()
}

func (a *App) handleDeviceSelfConfig(w http.ResponseWriter, r *http.Request) {
	cfg, version, err := a.devices.config(deviceIDFrom(r.Context()))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, checkinResult{ConfigVersion: version, Config: &cfg})
}
