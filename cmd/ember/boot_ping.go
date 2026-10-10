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

const bootHookPath = "/hooks/awtrix/boot"

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

func (a *App) expectedBootCallback() string {
	cfg := a.cfg.Load()
	return buildBootCallbackURL(outboundIP(clockHost(cfg.effectiveClockURL())), cfg.HTTP.Addr)
}

func (a *App) handleAwtrixBoot(w http.ResponseWriter, r *http.Request) {
	a.RepublishAll("device_boot")
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) ensureBootPingScript(ctx context.Context) {
	a.bootPingMu.Lock()
	defer a.bootPingMu.Unlock()

	if !a.cfg.Load().AWTRIX.BootPing {
		_, present, err := a.getScript(ctx, berry.BootPingName)
		if err != nil {
			a.clockJobFailed(ctx, "boot ping: device read failed", "err", err)
			return
		}
		if !present {
			return
		}
		if err := a.deleteScript(ctx, berry.BootPingName); err != nil {
			a.clockJobFailed(ctx, "boot ping: uninstall failed", "err", err)
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
		a.clockJobFailed(ctx, "boot ping: device read failed", "err", err)
		return
	}
	if present && cur == want {
		return
	}
	if err := a.putScript(ctx, berry.BootPingName, want); err != nil {
		a.clockJobFailed(ctx, "boot ping: install failed", "err", err)
		return
	}
	a.logger.Info("boot ping script provisioned to device",
		"name", berry.BootPingName, "callback", url, "replaced", present)
}

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
