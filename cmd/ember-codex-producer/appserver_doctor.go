package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// appServerReport is the doctor section for the Codex app-server daemon. It
// only reads: a stat of the socket, one short-lived passive connection
// (initialize + thread/loaded/list), and the daemon's settings.json.
func appServerReport(ctx context.Context, cfg Config) []string {
	out := []string{fmt.Sprintf("codex app-server: source %s (EMBER_CODEX_APPSERVER)", onOff(cfg.AppServerEnabled))}
	if _, err := os.Lstat(cfg.AppServerSocket); err != nil {
		out = append(out, "  socket: absent ("+cfg.AppServerSocket+"); the Codex TUI starts the daemon, Ember never does")
	} else if _, err := os.Stat(cfg.AppServerSocket); err != nil {
		out = append(out, "  socket: stale ("+cfg.AppServerSocket+": "+err.Error()+")")
	} else {
		out = append(out, "  socket: present ("+cfg.AppServerSocket+")")
		out = append(out, "  "+probeAppServer(ctx, cfg.AppServerSocket))
	}
	return append(out, "  "+updaterState(cfg.CodexHome))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func probeAppServer(ctx context.Context, sock string) string {
	ws, err := dialWS(ctx, sock)
	if err != nil {
		return "connect: FAILED (" + err.Error() + ")"
	}
	c := newRPCConn(ws)
	go c.readLoop(func(string, json.RawMessage) {})
	defer func() { _ = ws.Close(); <-c.done }()
	var init struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": appServerClientName, "title": "Ember doctor", "version": version},
		"capabilities": map[string]any{"optOutNotificationMethods": appServerOptOut},
	}, &init); err != nil {
		return "connect: FAILED (" + err.Error() + ")"
	}
	_ = c.notify("initialized")
	var ll struct {
		Data []string `json:"data"`
	}
	loaded := "?"
	if c.call(ctx, "thread/loaded/list", map[string]any{}, &ll) == nil {
		loaded = fmt.Sprint(len(ll.Data))
	}
	return fmt.Sprintf("connect: OK, daemon %q, %s loaded threads", init.UserAgent, loaded)
}

// updaterState reads $CODEX_HOME/app-server-daemon/settings.json; the daemon's
// self-updater is on unless updater.autoUpdateEnabled is false.
func updaterState(codexHome string) string {
	dir := filepath.Join(codexHome, "app-server-daemon")
	if _, err := os.Stat(dir); err != nil {
		return "updater: n/a (no " + dir + ")"
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return "updater: on (default, no settings.json)"
	}
	if err != nil {
		return "updater: unknown (" + err.Error() + ")"
	}
	var s struct {
		Updater struct {
			AutoUpdateEnabled *bool `json:"autoUpdateEnabled"`
		} `json:"updater"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "updater: unknown (settings.json: " + err.Error() + ")"
	}
	if s.Updater.AutoUpdateEnabled != nil && !*s.Updater.AutoUpdateEnabled {
		return "updater: off (settings.json)"
	}
	return "updater: on (settings.json)"
}
