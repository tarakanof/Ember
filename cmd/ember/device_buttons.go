package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

type buttonStatusResponse struct {
	ExpectedCallback   string `json:"expected_callback"`
	ConfiguredCallback string `json:"configured_callback"`
	Configured         bool   `json:"configured"`
	LastPressUnix      int64  `json:"last_press_unix"`
	SecondsSince       *int64 `json:"seconds_since"`
}

func buildCallbackURL(ip, addr string) string {
	if ip == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	return fmt.Sprintf("http://%s/hooks/awtrix/button", net.JoinHostPort(ip, port))
}

func clockHost(base string) string {
	if u, err := url.Parse(base); err == nil {
		if h := u.Hostname(); h != "" {
			return h
		}
	}
	return "8.8.8.8"
}

func outboundIP(host string) string {
	conn, err := net.Dial("udp", net.JoinHostPort(host, "80"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

func (a *App) expectedButtonCallback() string {
	cfg := a.cfg.Load()
	return buildCallbackURL(outboundIP(clockHost(cfg.effectiveClockURL())), cfg.HTTP.Addr)
}

func (a *App) handleDeviceButtons(w http.ResponseWriter, r *http.Request) {
	sys, _ := a.clock.readSystem(r.Context())
	writeJSON(w, http.StatusOK, a.buttonStatus(sys))
}

func (a *App) buttonStatus(sys map[string]any) buttonStatusResponse {
	expected := a.expectedButtonCallback()
	last := a.lastButtonAt.Load()
	resp := buttonStatusResponse{
		ExpectedCallback: expected,
		LastPressUnix:    last,
	}
	if sys != nil {
		if cb, ok := sys["buttonCallback"].(string); ok {
			resp.ConfiguredCallback = cb
			resp.Configured = cb != "" && cb == expected
		}
	}
	if last > 0 {
		s := time.Now().Unix() - last
		if s < 0 {
			s = 0
		}
		resp.SecondsSince = &s
	}
	return resp
}

func (a *App) handleDeviceButtonsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !a.decodeOrReject(w, r, &body, true) {
		return
	}
	cb := ""
	if body.Enabled {
		cb = a.expectedButtonCallback()
	}
	ctx, cancel := a.clock.writeContext(r.Context())
	defer cancel()
	written, err := a.clock.updateSystem(ctx, func(sys map[string]any) { sys["buttonCallback"] = cb })
	if err != nil {
		a.clock.writeBudgetError(ctx, w, err, false)
		return
	}
	sys, err := a.clock.readSystem(ctx)
	if err != nil && ctx.Err() != nil {
		sys = written
	}
	writeJSON(w, http.StatusOK, a.buttonStatus(sys))
}
