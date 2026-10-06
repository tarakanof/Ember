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

func requireDevice(a *App, next http.Handler) http.Handler {
	return deviceAuth(a, nil, next)
}

func requireControl(a *App, next http.Handler) http.Handler {
	return deviceAuthWith(a, next, next, scopeControl, false)
}

func rateLimitAuthFailures(a *App, owner, device http.Handler) http.Handler {
	return deviceAuthWith(a, owner, device, "", true)
}

const (
	viewBurst  = 30
	viewPerSec = 10.0
)

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
	return deviceAuthWith(a, owner, device, "", false)
}

func deviceAuthWith(a *App, owner, device http.Handler, clientScope string, chargeFailures bool) http.Handler {
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
		if clientScope != "" && isClientBearer(r) {
			if a.clientAuth(w, r, clientScope) {
				owner.ServeHTTP(w, r)
			}
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
		Kind   string   `json:"kind"`
		HwID   string   `json:"hw_id"`
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !a.requireMasterForClients(w, r) {
		return
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if req.Kind == deviceKindClient {
		a.createClient(w, r, req.HwID, req.Name, req.Scopes)
		return
	}
	if req.Kind != deviceKindKnob {
		a.writeDeviceError(w, r, fmt.Errorf("%w: kind must be %q or %q", errDeviceBody, deviceKindKnob, deviceKindClient))
		return
	}
	if req.Scopes != nil {
		a.writeDeviceError(w, r, fmt.Errorf("%w: scopes apply to kind %q only", errDeviceBody, deviceKindClient))
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

func (a *App) createClient(w http.ResponseWriter, r *http.Request, hwID, rawName string, rawScopes []string) {
	if hwID != "" {
		a.writeDeviceError(w, r, fmt.Errorf("%w: a client has no hw_id", errDeviceBody))
		return
	}
	name, err := normalizeDeviceName(rawName, true)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	scopes, err := normalizeScopes(rawScopes)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	view, token, err := a.devices.provisionClient(name, scopes)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "client token minted", "device_id", view.ID, "scopes", strings.Join(scopes, ","))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, mintedDevice{deviceView: view, Token: token})
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
	if a.devices.isClient(r.PathValue("id")) && !a.requireMasterForClients(w, r) {
		return
	}
	view, token, err := a.devices.rotate(r.PathValue("id"))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if token != "" {
		a.logger.InfoContext(r.Context(), "client token rotated", "device_id", view.ID)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, mintedDevice{deviceView: view, Token: token})
		return
	}
	a.logger.InfoContext(r.Context(), "device token rotation started", "device_id", view.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func (a *App) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.devices.isClient(id) && !a.requireMasterForClients(w, r) {
		return
	}
	if err := a.devices.remove(id); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.knobStats.forget(id)
	a.wifiDrops.forget(id)
	a.diagDrops.forget(id)
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
		Diag                json.RawMessage `json:"diag"`
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
		Wifi:                decodeCheckinPart[deviceWifi](a, r, "wifi", req.Wifi, &a.wifiDrops),
		Diag:                a.decodeDeviceDiag(r, req.Diag),
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
	if c := res.newCrash; c != nil {
		a.logger.WarnContext(r.Context(), "knob crash reported", "device_id", id, "reason", c.Reason, "task", c.Task, "pc", c.PC,
			"boots", report.Diag.Boots, "reset_reason", report.Diag.ResetReason)
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

type checkinPart interface {
	validate() error
}

func decodeCheckinPart[T checkinPart](a *App, r *http.Request, name string, raw json.RawMessage, drops *checkinDropLog) *T {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v T
	err := json.Unmarshal(raw, &v)
	if err == nil {
		err = v.validate()
	}
	id := deviceIDFrom(r.Context())
	if err != nil {
		level := slog.LevelDebug
		if drops.changed(id, err.Error()) {
			level = slog.LevelInfo
		}
		a.logger.Log(r.Context(), level, "device "+name+" dropped", "device_id", id, "err", err)
		return nil
	}
	drops.changed(id, "")
	return &v
}

func (a *App) decodeDeviceDiag(r *http.Request, raw json.RawMessage) *deviceDiag {
	d := decodeCheckinPart[deviceDiag](a, r, "diag", raw, &a.diagDrops)
	if d != nil {
		d.Reboots, d.PrevResetReason = 0, ""
	}
	return d
}

type checkinDropLog struct {
	mu   sync.Mutex
	last map[string]string
}

func (l *checkinDropLog) changed(id, reason string) bool {
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

func (l *checkinDropLog) forget(id string) {
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
