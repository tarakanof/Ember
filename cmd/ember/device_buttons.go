package main

import (
	"encoding/json"
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
	return buildCallbackURL(outboundIP(clockHost(cfg.AWTRIX.HTTPBaseURL)), cfg.HTTP.Addr)
}

func (a *App) handleDeviceButtons(w http.ResponseWriter, r *http.Request) {
	expected := a.expectedButtonCallback()
	last := a.lastButtonAt.Load()
	resp := buttonStatusResponse{
		ExpectedCallback: expected,
		LastPressUnix:    last,
	}
	// Best-effort: an unreachable clock still reports press tracking, just
	// without configured/configured_callback.
	if sys, err := a.readSystem(r.Context()); err == nil {
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
	writeJSON(w, http.StatusOK, resp)
}

// handleDeviceButtonsPut sets or clears the clock's buttonCallback via a
// read-merge-PUT of /api/v1/system (same safety rationale as
// handleDeviceSensorsPut — a naive partial PUT risks dropping Wi-Fi
// credentials). enabled:true points buttonCallback at this server's own
// callback URL; enabled:false clears it back to "".
func (a *App) handleDeviceButtonsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body, true); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sys, err := a.readSystem(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if body.Enabled {
		sys["buttonCallback"] = a.expectedButtonCallback()
	} else {
		sys["buttonCallback"] = ""
	}
	payload, err := json.Marshal(sys)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_, status, err := a.proxyToDevice(r.Context(), http.MethodPut, "/api/v1/system", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	a.handleDeviceButtons(w, r)
}
