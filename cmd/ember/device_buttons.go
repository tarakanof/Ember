package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// buttonStatusResponse is GET /v1/device/buttons. seconds_since is null until a
// button POST has been received. configured/configured_callback come from the
// clock's live /api/v1/system.buttonCallback (best-effort — a device read
// failure just leaves them zero-valued so button-press tracking still works).
type buttonStatusResponse struct {
	ExpectedCallback   string `json:"expected_callback"`
	ConfiguredCallback string `json:"configured_callback"`
	Configured         bool   `json:"configured"`
	LastPressUnix      int64  `json:"last_press_unix"`
	SecondsSince       *int64 `json:"seconds_since"`
}

// buildCallbackURL forms the buttonCallback the clock's /api/v1/system should
// hold. Empty when the IP or the listen port can't be determined.
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

// clockHost extracts the host from the configured clock URL, falling back to a
// public address so outboundIP can still resolve the primary egress interface.
func clockHost(base string) string {
	if u, err := url.Parse(base); err == nil {
		if h := u.Hostname(); h != "" {
			return h
		}
	}
	return "8.8.8.8"
}

// outboundIP returns the local IP the OS would use to reach host — i.e. the
// address the clock would see the server at. UDP "dial" sends nothing; it just
// resolves the route. Best-effort: "" on failure.
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
	// Best-effort: an unreachable clock still reports press tracking, just
	// without configured/configured_callback (readSystem's nil object).
	sys, _ := a.clock.readSystem(r.Context())
	writeJSON(w, http.StatusOK, a.buttonStatus(sys))
}

// buttonStatus reports press tracking and, when sys (the clock's system
// object) is non-nil, whether its buttonCallback points at this server.
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

// handleDeviceButtonsPut sets or clears the clock's buttonCallback via a
// read-merge-PUT of /api/v1/system (same safety rationale as
// handleDeviceSensorsPut — a naive partial PUT risks dropping Wi-Fi
// credentials). enabled:true points buttonCallback at this server's own
// callback URL; enabled:false clears it back to "". Budgeted like
// handleDeviceSensorsPut: 504 when the budget runs out before the PUT
// answered, the written object instead of a re-read after it.
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
