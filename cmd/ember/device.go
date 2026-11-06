package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/discovery"
)

const defaultDeviceBaseURL = "http://192.168.0.14"

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

func (a *App) initDeviceDiscovery(ctx context.Context) {
	_ = a.rediscoverClock(ctx)
	a.refreshCapabilities(ctx)
	a.provisionClockInBackground()
}

var errBrowseNotWired = errors.New("clock browse not wired")

type browseFunc func(context.Context, time.Duration) ([]discovery.Candidate, error)

func browseNotWired(context.Context, time.Duration) ([]discovery.Candidate, error) {
	return nil, errBrowseNotWired
}

func (a *App) rediscoverClock(ctx context.Context) bool {
	if clockDisabled() {
		a.lastRediscoverResult.Store("disabled")
		return false
	}
	a.deviceRediscoverMu.Lock()
	defer a.deviceRediscoverMu.Unlock()

	defer a.lastRediscoverAt.Store(time.Now().Unix())

	cur := a.cfg.Load().effectiveClockURL()
	if cur != "" {
		for i := 0; i < rediscoverProbeAttempts && ctx.Err() == nil; i++ {
			if a.clock.reachable(ctx, cur) {
				a.lastRediscoverResult.Store("reachable")
				return false
			}
		}
	}
	if ctx.Err() != nil {
		a.lastRediscoverResult.Store("cancelled")
		return false
	}

	cands, err := a.browseFn(ctx, 3*time.Second)
	if ctx.Err() != nil {
		a.lastRediscoverResult.Store("cancelled")
		return false
	}
	if err != nil || len(cands) == 0 {
		a.lastRediscoverResult.Store("no-device")
		a.logger.Info("clock discovery found no device", "configured", cur, "err", err)
		return false
	}
	base := cands[0].BaseURL
	if sameDeviceURL(base, cur) {
		a.lastRediscoverResult.Store("reachable")
		return false
	}
	if !a.swapDiscoveredClock(cur, base) {
		if sameDeviceURL(base, a.cfg.Load().effectiveClockURL()) {
			a.lastRediscoverResult.Store("reachable")
			return false
		}
		a.lastRediscoverResult.Store("superseded")
		a.logger.Info("clock discovery result dropped: clock URL changed during the browse", "configured", cur, "discovered", base)
		return false
	}
	a.lastRediscoverResult.Store("swapped")
	a.logger.Info("clock auto-discovered", "base_url", cands[0].BaseURL, "uid", cands[0].UID)
	a.observeClock(cands[0].UID, nil)
	a.caps.invalidate()
	a.refreshCapabilities(ctx)
	return true
}

func sameDeviceURL(x, y string) bool {
	norm := func(raw string) (string, bool) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", false
		}
		scheme := strings.ToLower(u.Scheme)
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[scheme]
		}
		path := strings.TrimRight(u.EscapedPath(), "/")
		return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port) + path, true
	}
	nx, okx := norm(x)
	ny, oky := norm(y)
	return okx && oky && nx == ny
}

const rediscoverProbeAttempts = 2

const deviceWatchInterval = 30 * time.Second

type deviceProbe struct {
	reachable bool
	uptimeSec int64
	at        time.Time
}

const (
	bootEstimateSlack = 2 * time.Second
)

func rebootDetected(last, cur deviceProbe) bool {
	if !last.reachable || !cur.reachable {
		return false
	}
	if cur.uptimeSec < last.uptimeSec {
		return true
	}
	expected := last.uptimeSec + int64(cur.at.Sub(last.at)/time.Second)
	return cur.uptimeSec+int64(devices.RebootUptimeSlack/time.Second) < expected
}

func (a *App) probeDevice(ctx context.Context, maxAge time.Duration) deviceProbe {
	dev := a.probeClockHealthWithin(ctx, time.Now(), maxAge)
	if dev == nil || !dev.Reachable || dev.UptimeSec == nil {
		return deviceProbe{}
	}
	return deviceProbe{reachable: true, uptimeSec: *dev.UptimeSec, at: dev.CheckedAt}
}

func (a *App) StartDeviceWatch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var last deviceProbe
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			last = a.watchTick(ctx, last, interval)
		}
	}
}

func (a *App) watchTick(ctx context.Context, last deviceProbe, interval time.Duration) deviceProbe {
	if a.rediscoverClock(ctx) {
		last = deviceProbe{}
		a.clockMoved("clock_rediscovered")
	}
	cur := a.probeDevice(ctx, interval/2)
	if !cur.reachable {
		return last
	}
	if rebootDetected(last, cur) {
		a.logger.Info("clock reboot detected",
			"uptime_seconds", cur.uptimeSec,
			"prev_uptime_seconds", last.uptimeSec,
			"since_prev_seconds", int64(cur.at.Sub(last.at)/time.Second))
		a.clockRebooted("clock_reboot")
	} else if _, ok := a.capabilities(); !ok {
		a.refreshCapabilities(ctx)
	}
	return cur
}

const republishMinGap = 10 * time.Second

type republishGate struct {
	mu      sync.Mutex
	minGap  time.Duration
	last    time.Time
	pending bool
}

func (g *republishGate) admit(now time.Time, deferred func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	gap := g.minGap
	if gap == 0 {
		gap = republishMinGap
	}
	since := now.Sub(g.last)
	if g.last.IsZero() || since >= gap {
		g.last = now
		return true
	}
	if !g.pending {
		g.pending = true
		time.AfterFunc(gap-since, func() {
			g.mu.Lock()
			g.pending = false
			g.last = time.Now()
			g.mu.Unlock()
			deferred()
		})
	}
	return false
}

func (a *App) RepublishAll(reason string) {
	if a.coord == nil {
		return
	}
	if !a.republish.admit(time.Now(), func() { a.sendRepublish(reason, true) }) {
		a.logger.Debug("republish coalesced", "reason", reason)
		return
	}
	a.sendRepublish(reason, false)
}

func (a *App) sendRepublish(reason string, deferred bool) {
	a.logger.Info("republishing device state", "reason", reason, "deferred", deferred)
	a.coord.Send(coordCmd{kind: cmdRepublish})
}

func (a *App) handleDeviceConfigGet(w http.ResponseWriter, r *http.Request) {
	url, src := a.cfg.Load().clockURL()
	writeJSON(w, http.StatusOK, map[string]string{
		"base_url": url,
		"source":   src,
	})
}

func (a *App) handleDeviceConfigPut(w http.ResponseWriter, r *http.Request) {
	moved := false
	pin := func(patch []byte) func(Config, *Config) {
		var named clockConfigDTO
		pinned := json.Unmarshal(patch, &named) == nil && named.BaseURL != nil
		return func(before Config, c *Config) {
			if pinned {
				c.AWTRIX.clockDiscovered = ""
			}
			moved = !sameDeviceURL(before.effectiveClockURL(), c.effectiveClockURL())
		}
	}
	if _, ok := serveSettingPutWith(a, w, r, a.settings.clock, pin); !ok {
		return
	}
	a.caps.invalidate()
	if moved {
		a.clockMoved("clock_url_changed")
	}
	a.handleDeviceConfigGet(w, r)
}

func (a *App) handleDeviceDiscover(w http.ResponseWriter, r *http.Request) {
	if clockDisabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errClockDisabled.Error(), "code": "clock_disabled"})
		return
	}
	cands, err := a.browseFn(r.Context(), 3*time.Second)
	if r.Context().Err() != nil {
		a.logger.InfoContext(r.Context(), "clock discover request dropped: client went away during the browse")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if cands == nil {
		cands = []discovery.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": cands,
		"effective":  a.cfg.Load().effectiveClockURL(),
		"source":     a.deviceSource(),
	})
}
