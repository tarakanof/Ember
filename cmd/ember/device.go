package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
)

// deviceBaseURLKey is the writable-store key holding a menu-chosen clock URL.
// It overrides the read-only config.json baseline (mirrors the weather/Pomodoro
// config-persistence pattern).
const deviceBaseURLKey = "device_base_url"

// defaultDeviceBaseURL is the fallback clock URL used both when config.json
// omits awtrix.http_base_url (see Config.applyDefaults) and when a
// hand-edited baseline fails validDeviceURL (see sanitizeConfigBaseline).
const defaultDeviceBaseURL = "http://192.168.0.14"

// validDeviceURL reports (via a non-nil error) whether raw is unsafe to use as
// the clock's base URL. The /v1/device/* proxies forward requests to this URL
// verbatim, so it must be an absolute http/https URL with a non-empty host —
// otherwise a file:, gopher:, or bare-path value could be used for SSRF or to
// read local files. Applied to both the PUT /v1/device/config body and the
// config.json baseline (see sanitizeConfigBaseline).
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

// applyDeviceBaseURL validates a clock base URL, swaps it into the live config,
// and persists it to the store. Not a settings-overlay setting: it is a raw
// string, discovery swaps it in memory, and /admin/reload re-applies it only
// when the file URL changed (see admin.go).
func (a *App) applyDeviceBaseURL(raw string) error {
	if err := validDeviceURL(raw); err != nil {
		return err
	}
	a.updateConfig(func(cur *Config) { cur.AWTRIX.HTTPBaseURL = raw })
	if a.store != nil {
		if err := a.store.PutSetting(deviceBaseURLKey, raw); err != nil {
			a.logger.Warn("device base url persist failed", "err", err)
		}
	}
	return nil
}

// loadPersistedDeviceBaseURL applies a previously menu-chosen clock URL.
func (a *App) loadPersistedDeviceBaseURL() {
	if a.store == nil {
		return
	}
	if v, ok, err := a.store.GetSetting(deviceBaseURLKey); err == nil && ok && v != "" {
		_ = a.applyDeviceBaseURL(v)
	}
}

// deviceSource reports where the effective clock URL came from:
// "store" (menu override) > "config" (config.json baseline) > "discovered"
// (mDNS auto-pick) > "none".
func (a *App) deviceSource() string {
	a.cfgMu.Lock()
	cur, baseline := a.cfg.Load().AWTRIX.HTTPBaseURL, a.deviceBaseline
	a.cfgMu.Unlock()
	if a.store != nil {
		// A stored override only reflects the current effective URL if it
		// still matches it — rediscoverClock can swap away from a stale
		// store override in-memory without touching the store entry.
		if v, ok, _ := a.store.GetSetting(deviceBaseURLKey); ok && v == cur {
			return "store"
		}
	}
	switch {
	case cur == "":
		return "none"
	case a.deviceAutoPicked.Load():
		// Discovery set this URL at boot — even if it happens to equal the
		// (unreachable) config.json baseline, it was reached via discovery.
		return "discovered"
	case cur == baseline:
		return "config"
	default:
		return "discovered"
	}
}

// initDeviceDiscovery runs once at boot (and can be re-run by the periodic
// probe): it delegates to rediscoverClock, which checks the current effective
// clock URL — regardless of whether it came from a store override,
// config.json, or a prior discovery — and falls back to mDNS auto-discovery
// if it's unreachable. A stale store override no longer permanently blocks
// re-discovery: it's just another URL that gets checked for reachability.
func (a *App) initDeviceDiscovery(ctx context.Context) {
	a.loadPersistedDeviceBaseURL()
	_ = a.rediscoverClock(ctx)
	a.refreshCapabilities(ctx)
}

// rediscoverClock checks whether the current effective clock URL is
// reachable; if not, it browses mDNS and swaps in the first reachable
// candidate (in-memory only — never persisted to config.json or the store).
// Returns true if the URL changed. Records the attempt (time + outcome) for
// /admin/doctor. Safe to call from boot and from a periodic probe; callers
// are serialized via deviceRediscoverMu so two browses can't race.
//
// The current URL gets rediscoverProbeAttempts tries before the browse: the
// server→clock link drops a large share of requests, and one lost 1.5s GET is
// not evidence that the clock moved.
func (a *App) rediscoverClock(ctx context.Context) bool {
	a.deviceRediscoverMu.Lock()
	defer a.deviceRediscoverMu.Unlock()

	defer a.lastRediscoverAt.Store(time.Now().Unix())

	cl := &http.Client{Timeout: 1500 * time.Millisecond}
	cur := a.cfg.Load().AWTRIX.HTTPBaseURL
	if cur != "" {
		for i := 0; i < rediscoverProbeAttempts && ctx.Err() == nil; i++ {
			if _, ok := discovery.Reachable(ctx, cl, cur); ok {
				a.lastRediscoverResult.Store("reachable")
				return false
			}
		}
	}

	browse := a.browseFn
	if browse == nil {
		browse = discovery.BrowseAWTRIX
	}
	cands, err := browse(ctx, 3*time.Second)
	if err != nil || len(cands) == 0 {
		a.lastRediscoverResult.Store("no-device")
		a.logger.Info("clock discovery found no device", "configured", a.cfg.Load().AWTRIX.HTTPBaseURL)
		return false
	}
	base := cands[0].BaseURL
	if sameDeviceURL(base, cur) {
		// Probes were lost but the clock didn't move: not a swap, no republish.
		a.lastRediscoverResult.Store("reachable")
		return false
	}
	a.updateConfig(func(cur *Config) { cur.AWTRIX.HTTPBaseURL = base }) // in-memory only; not persisted
	a.deviceAutoPicked.Store(true)
	a.lastRediscoverResult.Store("swapped")
	a.logger.Info("clock auto-discovered", "base_url", cands[0].BaseURL, "uid", cands[0].UID)
	// A different clock can be a different firmware build: re-read its
	// capabilities rather than serving the previous device's lists.
	a.refreshCapabilities(ctx)
	return true
}

// sameDeviceURL reports whether two clock base URLs address the same endpoint.
// Discovery always builds "http://<ip>:<port>", while config.json usually omits
// the default port, so a plain string compare would call the same clock a swap.
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

// rediscoverProbeAttempts is how many reachability GETs rediscoverClock spends
// on the current URL before it falls back to an mDNS browse.
const rediscoverProbeAttempts = 2

// deviceWatchInterval is how often the watcher probes the clock — both for
// reachability (self-healing re-discovery) and for uptimeSeconds (reboot
// detection). It is deliberately 30s, not 60s: this loop replaced the blind 30s
// Pomodoro takeover re-assert, so polling at the old re-assert cadence keeps
// worst-case recovery latency after a reboot comparable. The Berry boot-ping
// hook (issue #73) will make recovery instant and let this relax again.
const deviceWatchInterval = 30 * time.Second

// deviceProbeTimeout bounds one GET /api/v1/device in the watch loop. Same
// budget as the re-discovery reachability probe — the loop must never outlive
// its own tick.
const deviceProbeTimeout = 1500 * time.Millisecond

// deviceProbe is one device-watch observation: whether the clock answered, the
// uptime it reported, and when we read it.
type deviceProbe struct {
	reachable bool
	uptimeSec int64
	at        time.Time
}

// rebootUptimeSlack absorbs the jitter between our wall clock and the device's
// uptime counter: whole-second truncation on the device, plus up to one probe
// timeout of latency on each of the two readings.
const rebootUptimeSlack = 10 * time.Second

// rebootDetected reports whether the clock restarted since last, the most
// recent probe that got an answer. Only the uptime counter decides. A missed
// probe says nothing: on the lossy server→clock link a dropped GET is routine,
// and treating "silent, then answering" as a reboot republished (and
// re-switched the screen) every few ticks.
//
// A reboot shows as uptime falling behind wall time. Usually the counter also
// goes backwards, but a reboot during a gap longer than the old uptime leaves
// it above last.uptimeSec while still well short of where it would be had the
// clock stayed up. A zero last (no answer yet this process) is never a reboot:
// nothing has been pushed yet that a reboot could have dropped.
func rebootDetected(last, cur deviceProbe) bool {
	if !last.reachable || !cur.reachable {
		return false
	}
	if cur.uptimeSec < last.uptimeSec {
		return true
	}
	expected := last.uptimeSec + int64(cur.at.Sub(last.at)/time.Second)
	return cur.uptimeSec+int64(rebootUptimeSlack/time.Second) < expected
}

// probeDevice fetches GET /api/v1/device from the currently-effective clock URL.
// Any failure (no URL, timeout, non-2xx) reports an unreachable probe rather
// than an error: the caller only needs the reachable/uptime pair.
func (a *App) probeDevice(ctx context.Context) deviceProbe {
	base := a.cfg.Load().AWTRIX.HTTPBaseURL
	if base == "" {
		return deviceProbe{}
	}
	info, err := awtrix.NewClient(base, deviceProbeTimeout).DeviceInfo(ctx)
	if err != nil {
		return deviceProbe{}
	}
	return deviceProbe{reachable: true, uptimeSec: info.UptimeSeconds, at: time.Now()}
}

// StartDeviceWatch runs the periodic self-healing probe loop until ctx is
// done. Each tick calls rediscoverClock (a no-op when the current effective
// clock URL is already reachable), then reads the device's uptime to notice a
// reboot and trigger a republish. A swap to a new URL republishes too: pushes
// to the old address were failing, and the uptime baseline belonged to it.
// Callers should gate the goroutine on AWTRIXConfig.AutoRediscoverEnabled();
// the loop itself runs unconditionally once started.
func (a *App) StartDeviceWatch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var last deviceProbe // most recent probe that got an answer
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.rediscoverClock(ctx) {
				last = deviceProbe{}
				a.RepublishAll("clock_rediscovered")
			}
			cur := a.probeDevice(ctx)
			if !cur.reachable {
				continue
			}
			if rebootDetected(last, cur) {
				a.logger.Info("clock reboot detected",
					"uptime_seconds", cur.uptimeSec,
					"prev_uptime_seconds", last.uptimeSec,
					"since_prev_seconds", int64(cur.at.Sub(last.at)/time.Second))
				a.RepublishAll("clock_reboot")
			}
			last = cur
		}
	}
}

// republishMinGap is the least time between two republishes. A republish
// clears every dedupe entry and re-pushes the frame, the tiles, the indicators
// and the hold switch to a clock on a lossy link, so the unauthenticated boot
// hook must not be able to queue one per request. 10s is shorter than any real
// reboot cycle (the boot ping lands ~12s after a reboot).
const republishMinGap = 10 * time.Second

// republishGate spaces out RepublishAll with a leading and a trailing edge:
// the first request goes out at once, and requests inside the gap collapse
// into one deferred republish at the gap's end. Deferring rather than dropping
// matters when the clock reboots twice in quick succession: the second boot's
// state still has to be pushed back.
type republishGate struct {
	mu      sync.Mutex
	minGap  time.Duration // 0 means republishMinGap; tests shorten it
	last    time.Time
	pending bool
}

// admit reports whether a republish may go out now. When it may not, it
// arranges for deferred to run once the gap has elapsed (at most one pending
// at a time) and returns false.
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

// RepublishAll asks the coordinator to forget what it believes the device is
// showing and push everything again on its next (immediate) cycle: the active
// app frame, the standalone tiles, and the Pomodoro takeover if a timer is
// running. Safe to call from any goroutine — the work happens on the
// coordinator goroutine. reason is logged. This is the single entry point for
// "the device lost our state"; issue #73's device boot-ping hook calls it
// directly instead of waiting for the watch loop to notice.
//
// Calls closer together than republishMinGap are coalesced (see
// republishGate): the first is sent at once, the rest become one deferred
// republish.
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
	writeJSON(w, http.StatusOK, map[string]string{
		"base_url": a.cfg.Load().AWTRIX.HTTPBaseURL,
		"source":   a.deviceSource(),
	})
}

func (a *App) handleDeviceConfigPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
	}
	if !a.decodeOrReject(w, r, &body, false) {
		return
	}
	if err := a.applyDeviceBaseURL(body.BaseURL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The cached capabilities described the previous clock. Emptying the
	// cache sends the next capabilities read to the new clock and lets the
	// audio routes ask it rather than refuse on the old one's word.
	a.caps.Store(nil)
	a.handleDeviceConfigGet(w, r)
}

func (a *App) handleDeviceDiscover(w http.ResponseWriter, r *http.Request) {
	browse := a.browseFn
	if browse == nil {
		browse = discovery.BrowseAWTRIX
	}
	cands, err := browse(r.Context(), 3*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if cands == nil {
		cands = []discovery.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": cands,
		"effective":  a.cfg.Load().AWTRIX.HTTPBaseURL,
		"source":     a.deviceSource(),
	})
}
