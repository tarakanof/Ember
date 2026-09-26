package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/berry"
)

// bootHookPath is the device-only hook the on-clock boot-ping script POSTs to
// once after every reboot.
const bootHookPath = "/hooks/awtrix/boot"

// buildBootCallbackURL forms the URL the boot-ping script should POST to — the
// address the clock would see this server at. Empty when the IP or the listen
// port can't be determined.
//
// Deliberately a twin of buildCallbackURL (device_buttons.go) rather than a
// shared, path-parameterised builder: two hooks with two paths, and eight
// duplicated lines read better than an abstraction with one caller each.
func buildBootCallbackURL(ip, addr string) string {
	if ip == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(ip, port) + bootHookPath
}

// expectedBootCallback derives the boot hook URL exactly as
// expectedButtonCallback derives the button one: the local address the OS would
// use to reach the clock, plus this server's listen port.
func (a *App) expectedBootCallback() string {
	cfg := a.cfg.Load()
	return buildBootCallbackURL(outboundIP(clockHost(cfg.AWTRIX.HTTPBaseURL)), cfg.HTTP.Addr)
}

// handleAwtrixBoot ingests the boot ping from the clock's ember-boot-ping
// script: an empty, fire-and-forget POST, once per device boot.
//
// Unauthenticated for the same reason as /hooks/awtrix/button — a script on the
// clock has nowhere to keep a bearer token that the device's own
// GET /api/v1/apps/script/{name} wouldn't hand to anyone on the LAN. The blast
// radius is a republish of state the server already owns: the coordinator
// re-pushes the frames it would have pushed anyway when its 30 s watch loop
// noticed the reboot. So an unauthenticated caller can make the server talk to
// the clock it already talks to, and nothing else. A republish is heavy on the
// lossy link, though, so a flood is bounded twice: the route sits behind the
// per-IP rate limiter, and RepublishAll coalesces anything closer together than
// republishMinGap. The body is never read.
//
// Answers 204 immediately; the republish happens on the coordinator goroutine.
func (a *App) handleAwtrixBoot(w http.ResponseWriter, r *http.Request) {
	a.RepublishAll("device_boot")
	w.WriteHeader(http.StatusNoContent)
}

// ensureBootPingScript brings the clock's ember-boot-ping script in line with
// awtrix.boot_ping: installed and carrying this server's current boot-hook URL
// when the toggle is on, removed when it is off. Called at startup and after
// /admin/reload — the same provisioning shape as ensureNativeIcons.
//
// Best-effort throughout: every failure is logged and left for the next run,
// because a clock that is unreachable at startup is normal and the feature is
// an optimisation over the watch loop, not a dependency of it. Safe to call
// from any goroutine; runs are serialised by bootPingMu.
func (a *App) ensureBootPingScript(ctx context.Context) {
	a.bootPingMu.Lock()
	defer a.bootPingMu.Unlock()

	if !a.cfg.Load().AWTRIX.BootPing {
		_, present, err := a.getScript(ctx, berry.BootPingName)
		if err != nil {
			a.logger.Warn("boot ping: device read failed", "err", err)
			return
		}
		if !present {
			return
		}
		if err := a.deleteScript(ctx, berry.BootPingName); err != nil {
			a.logger.Warn("boot ping: uninstall failed", "err", err)
			return
		}
		a.logger.Info("boot ping script removed from device", "name", berry.BootPingName)
		return
	}

	url := a.expectedBootCallback()
	if url == "" {
		a.logger.Warn("boot ping: no callback URL derivable, install skipped")
		return
	}
	want := berry.BootPingSource(url)
	cur, present, err := a.getScript(ctx, berry.BootPingName)
	if err != nil {
		a.logger.Warn("boot ping: device read failed", "err", err)
		return
	}
	// Re-PUTting restarts the app on the device (and would re-ping), so an
	// already-current script is left alone.
	if present && cur == want {
		return
	}
	if err := a.putScript(ctx, berry.BootPingName, want); err != nil {
		a.logger.Warn("boot ping: install failed", "err", err)
		return
	}
	a.logger.Info("boot ping script provisioned to device",
		"name", berry.BootPingName, "callback", url, "replaced", present)
}

// getScript returns the Berry source the clock currently holds under name.
// present is false (with a nil error) when the device has no such script.
func (a *App) getScript(ctx context.Context, name string) (source string, present bool, err error) {
	reply, err := a.clock.raw(ctx, func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
		return cl.RawScript(ctx, name)
	})
	if err != nil {
		return "", false, err
	}
	switch {
	case reply.Status == http.StatusNotFound:
		return "", false, nil
	case reply.Status != http.StatusOK:
		return "", false, fmt.Errorf("clock returned %d", reply.Status)
	}
	return string(reply.Body), true, nil
}

// putScript uploads Berry source under name as text/plain.
//
// A script that fails to compile still installs: the device answers 200 with
// the compiler message in the reply's "error" and renders ERR:<name> on the
// panel until a good source replaces it. So the reply body — not the status —
// is what says the install worked, and a non-null "error" is returned as one.
func (a *App) putScript(ctx context.Context, name, source string) error {
	raw, err := a.clock.raw(ctx, func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
		return cl.RawPutScript(ctx, name, source)
	})
	if err != nil {
		return err
	}
	if raw.Status < 200 || raw.Status >= 300 {
		return fmt.Errorf("clock returned %d: %s", raw.Status, strings.TrimSpace(string(raw.Body)))
	}
	var reply struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw.Body, &reply); err == nil && len(reply.Error) > 0 && string(reply.Error) != "null" {
		return fmt.Errorf("script did not compile: %s", reply.Error)
	}
	return nil
}

// deleteScript removes the app (script included) from the clock. A device that
// no longer holds it is success, not a failure.
func (a *App) deleteScript(ctx context.Context, name string) error {
	reply, err := a.clock.raw(ctx, func(cl *awtrix.Client, ctx context.Context) (awtrix.Reply, error) {
		return cl.RawDeleteApp(ctx, name)
	})
	if err != nil {
		return err
	}
	if reply.Status == http.StatusNotFound {
		return nil
	}
	if reply.Status < 200 || reply.Status >= 300 {
		return fmt.Errorf("clock returned %d", reply.Status)
	}
	return nil
}
