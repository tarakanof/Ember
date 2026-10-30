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

	"github.com/tarakanof/ember/internal/devices"
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
			if _, ok := a.clientAuth(w, r, clientScope); ok {
				owner.ServeHTTP(w, r)
			}
			return
		}
		bearer, _ := strings.CutPrefix(header, "Bearer ")
		id, ok, err := a.devices.Authenticate(bearer)
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
		if c, collected := a.claims.Collect(bearer); collected {
			a.logger.InfoContext(r.Context(), "device claim collected", "claim_id", c.ID, "hw_id", c.HwID, "device_id", id)
		}
		device.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceIDKey{}, id)))
	})
}

func (a *App) writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, devices.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, devices.ErrSettingBody), errors.Is(err, devices.ErrBody):
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path, "reason", "invalid")
		writeError(w, http.StatusBadRequest, err)
	default:
		a.logger.WarnContext(r.Context(), "device registry write failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("device registry write failed"))
	}
}

type mintedDevice struct {
	devices.View
	Token string `json:"token"`
}

func (a *App) handleDevicesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string   `json:"kind"`
		HwID    string   `json:"hw_id"`
		Name    string   `json:"name"`
		Scopes  []string `json:"scopes"`
		Sources []string `json:"sources"`
	}
	if !a.requireMasterForClients(w, r) {
		return
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if req.Kind == devices.KindClient {
		a.createClient(w, r, req.HwID, req.Name, req.Scopes, req.Sources)
		return
	}
	if req.Kind != devices.KindKnob {
		a.writeDeviceError(w, r, fmt.Errorf("%w: kind must be %q or %q", devices.ErrBody, devices.KindKnob, devices.KindClient))
		return
	}
	if req.Scopes != nil || req.Sources != nil {
		a.writeDeviceError(w, r, fmt.Errorf("%w: scopes and sources apply to kind %q only", devices.ErrBody, devices.KindClient))
		return
	}
	hwID, err := devices.NormalizeHwID(req.HwID)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	name, err := devices.NormalizeName(req.Name, false)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	view, token, created, err := a.devices.Provision(hwID, name)
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
	writeJSON(w, status, mintedDevice{View: view, Token: token})
}

func (a *App) createClient(w http.ResponseWriter, r *http.Request, hwID, rawName string, rawScopes, rawSources []string) {
	if hwID != "" {
		a.writeDeviceError(w, r, fmt.Errorf("%w: a client has no hw_id", devices.ErrBody))
		return
	}
	name, err := devices.NormalizeName(rawName, true)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	scopes, err := normalizeScopes(rawScopes)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	sources, err := normalizeSources(rawSources, scopes)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	view, token, err := a.clients.Mint(name, scopes, sources)
	if err != nil {
		a.writeClientError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "client token minted", "device_id", view.ID,
		"scopes", strings.Join(scopes, ","), "sources", strings.Join(sources, ","))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, mintedClient{ClientView: view, Token: token})
}

type mintedClient struct {
	devices.ClientView
	Token string `json:"token"`
}

func (a *App) handleDevicesList(w http.ResponseWriter, r *http.Request) {
	views := a.devices.List()
	for i := range views {
		views[i] = a.withShown(views[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": views})
}

func (a *App) handleClientsList(w http.ResponseWriter, r *http.Request) {
	if err := a.clients.LoadError(); err != nil {
		a.writeClientError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": a.clients.List()})
}

func isClientID(id string) bool {
	return strings.HasPrefix(id, devices.ClientIDPrefix)
}

func (a *App) writeClientError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, devices.ErrNotFound) || errors.Is(err, devices.ErrBody) {
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.WarnContext(r.Context(), "client token store failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, errors.New("client token store unavailable"))
}

func (a *App) handleDeviceConfigGetOwner(w http.ResponseWriter, r *http.Request) {
	if kind, _ := a.devices.KindOf(r.PathValue("id")); kind == devices.KindClock {
		a.handleClockConfigGet(w, r)
		return
	}
	cfg, version, err := a.devices.Config(r.PathValue("id"))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	w.Header().Set(deviceConfigVersion, strconv.Itoa(version))
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleDeviceConfigPutOwner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if kind, _ := a.devices.KindOf(id); kind == devices.KindClock {
		a.handleClockConfigPut(w, r, id)
		return
	}
	var patch json.RawMessage
	if !a.decodeOrReject(w, r, &patch, false) {
		return
	}
	cfg, version, changed, err := a.devices.PutConfig(id, patch)
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
	id := r.PathValue("id")
	if isClientID(id) && !a.requireMasterForClients(w, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	name, err := devices.NormalizeName(req.Name, true)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if isClientID(id) {
		view, err := a.clients.Rename(id, name)
		if err != nil {
			a.writeClientError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	view, err := a.devices.Rename(id, name)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.withShown(view))
}

func (a *App) handleDeviceRotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if isClientID(id) {
		a.rotateClient(w, r, id)
		return
	}
	view, err := a.devices.Rotate(id)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device token rotation started", "device_id", view.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func (a *App) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if isClientID(id) {
		a.deleteClient(w, r, id)
		return
	}
	if err := a.devices.Remove(id); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.knobStats.forget(id)
	a.wifiDrops.forget(id)
	a.diagDrops.forget(id)
	a.idleDrops.forget(id)
	a.roomDrops.forget(id)
	a.otaDrops.forget(id)
	a.capsDrops.forget(id)
	a.viewOverLimit.forget(id)
	a.knobShown.forget(id)
	a.shownDrops.forget(id)
	a.ota.forget(id)
	if a.coredumps != nil {
		if err := a.coredumps.removeDevice(id); err != nil {
			a.logger.WarnContext(r.Context(), "knob coredumps not removed", "device_id", id, "err", err)
		}
	}
	a.logger.InfoContext(r.Context(), "device deleted", "device_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) rotateClient(w http.ResponseWriter, r *http.Request, id string) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	view, token, err := a.clients.Rotate(id)
	if err != nil {
		a.writeClientError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "client token rotated", "device_id", view.ID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, mintedClient{ClientView: view, Token: token})
}

func (a *App) deleteClient(w http.ResponseWriter, r *http.Request, id string) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	if err := a.clients.Remove(id); err != nil {
		a.writeClientError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "client token deleted", "device_id", id)
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
		FWBuild             string          `json:"fw_build"`
		OTA                 json.RawMessage `json:"ota"`
		Caps                json.RawMessage `json:"caps"`
		Room                json.RawMessage `json:"room"`
	}
	if !a.decodeOptionalOrReject(w, r, &req, false) {
		return
	}
	if utf8.RuneCountInString(req.FW) > 32 || !utf8.ValidString(req.FW) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: fw must be at most 32 characters", devices.ErrBody))
		return
	}
	if req.LinkMHz < 0 || req.LinkMHz > 1000 {
		a.writeDeviceError(w, r, fmt.Errorf("%w: link_mhz must be 0..1000", devices.ErrBody))
		return
	}
	ip := clientIP(r)
	if req.IP != "" {
		addr, err := netip.ParseAddr(req.IP)
		if err != nil {
			a.writeDeviceError(w, r, fmt.Errorf("%w: ip must be an IP address", devices.ErrBody))
			return
		}
		ip = addr.String()
	}
	id := deviceIDFrom(r.Context())
	stats := a.decodeKnobStats(r, req.Stats)
	report := devices.Checkin{
		FW:                  req.FW,
		IP:                  ip,
		RSSI:                req.RSSI,
		HeapInternalFree:    req.HeapInternalFree,
		HeapInternalLargest: req.HeapInternalLargest,
		UptimeS:             req.UptimeS,
		AppliedVersion:      req.ConfigVersion,
		LinkMHz:             req.LinkMHz,
		LinkFallback:        req.LinkFallback && req.LinkMHz > 0,
		Wifi:                decodeCheckinPart[devices.Wifi](a, r, "wifi", req.Wifi, &a.wifiDrops),
		Diag:                a.decodeDeviceDiag(r, req.Diag),
		OTA:                 decodeCheckinPart[devices.KnobOTAReport](a, r, "ota", req.OTA, &a.otaDrops),
		Room:                a.decodeDeviceRoom(r, req.Room),
	}
	if devices.FirmwareBuildPattern.MatchString(req.FWBuild) {
		report.FWBuild = req.FWBuild
	}
	caps, capsErr := decodeCaps(req.Caps)
	if a.capsDrops.changed(id, capsErr) && capsErr != "" {
		a.logger.WarnContext(r.Context(), "device caps dropped", "device_id", id, "err", capsErr)
	}
	demoGen := a.demos.generation(id)
	res, err := a.devices.Checkin(id, report, caps, capsErr)
	if errors.Is(err, devices.ErrCheckinNotStored) {
		a.logger.WarnContext(r.Context(), "device checkin not persisted", "device_id", id, "err", err)
		err = nil
	}
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if c := res.NewCrash; c != nil {
		a.logger.WarnContext(r.Context(), "knob crash reported", "device_id", id, "reason", c.Reason, "task", c.Task, "pc", c.PC,
			"boots", report.Diag.Boots, "reset_reason", report.Diag.ResetReason)
	}
	if !res.BootedAt.IsZero() {
		a.noteKnobReboot(r, id, demoGen, res.BootedAt)
	}
	now := a.knobStats.now()
	if diag, _, err := a.devices.Diagnostics(id); err == nil {
		if stats = stats.forLevel(diag); stats != nil && !res.Stale {
			sample := knobSampleFromReport(report, stats)
			sample.BrightnessLevel = refOf(a.currentBrightness(now).Level)
			a.knobStats.record(id, now, sample)
		}
		res.DiagLiveUntil = a.knobLiveUnix(id, diag, now)
	}
	a.coredumpReply(id, report.Diag, &res)
	if res.Stale {
		a.logger.InfoContext(r.Context(), "stale device checkin ignored", "device_id", id, "uptime_s", report.UptimeS)
	} else if err := a.otaCheckin(id, report, &res); err != nil {
		a.logger.WarnContext(r.Context(), "knob ota step failed", "device_id", id, "err", err)
	}
	if res.NewToken != "" {
		a.logger.InfoContext(r.Context(), "device rotation token issued", "device_id", id)
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set(knobNowHeader, unixHeader(now))
	writeJSON(w, http.StatusOK, res)
}

type checkinPart interface {
	Validate() error
}

func decodeCheckinPart[T checkinPart](a *App, r *http.Request, name string, raw json.RawMessage, drops *checkinDropLog) *T {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v T
	err := json.Unmarshal(raw, &v)
	if err == nil {
		err = v.Validate()
	}
	a.noteCheckinDrop(r, name, err, drops)
	if err != nil {
		return nil
	}
	return &v
}

func (a *App) noteCheckinDrop(r *http.Request, name string, err error, drops *checkinDropLog) {
	id := deviceIDFrom(r.Context())
	if err == nil {
		drops.changed(id, "")
		return
	}
	level := slog.LevelDebug
	if drops.changed(id, err.Error()) {
		level = slog.LevelInfo
	}
	a.logger.Log(r.Context(), level, "device "+name+" dropped", "device_id", id, "err", err)
}

func (a *App) decodeDeviceDiag(r *http.Request, raw json.RawMessage) *devices.Diag {
	var rawIdle json.RawMessage
	var parts map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		if rawIdle = parts["idle"]; rawIdle != nil {
			delete(parts, "idle")
			raw, _ = json.Marshal(parts)
		}
	}
	d := decodeCheckinPart[devices.Diag](a, r, "diag", raw, &a.diagDrops)
	if d != nil {
		d.Reboots, d.PrevResetReason, d.RebootsSinceSeen = 0, "", 0
		d.Idle = decodeCheckinPart[devices.Idle](a, r, "diag.idle", rawIdle, &a.idleDrops)
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
	cfg, version, err := a.devices.Config(deviceIDFrom(r.Context()))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, devices.CheckinResult{ConfigVersion: version, Config: &cfg})
}
