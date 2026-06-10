package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// buttonStatusResponse is GET /v1/device/buttons. seconds_since is null until a
// button POST has been received.
type buttonStatusResponse struct {
	ExpectedCallback string `json:"expected_callback"`
	LastPressUnix    int64  `json:"last_press_unix"`
	SecondsSince     *int64 `json:"seconds_since"`
}

// buildCallbackURL forms the button_callback the clock's dev.json should hold.
// Empty when the IP or the listen port can't be determined.
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
	last := a.lastButtonAt.Load()
	resp := buttonStatusResponse{
		ExpectedCallback: a.expectedButtonCallback(),
		LastPressUnix:    last,
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
