package discovery

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/brutella/dnssd"
)

// emberServiceType is the DNS-SD type the Ember server advertises so the macOS
// app can find it on the LAN.
const emberServiceType = "_ember._tcp"

// AdvertiseEnabled reports whether EMBER_MDNS_ADVERTISE enables advertising.
// Empty (unset) defaults to true; "0"/"false"/"no"/"off" disable it.
func AdvertiseEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// PortFromAddr extracts the numeric port from a listen address like ":8080" or
// "0.0.0.0:9000".
func PortFromAddr(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// Advertise announces an _ember._tcp service until ctx is cancelled. The TXT
// record carries the server version and a health path so clients can confirm
// the endpoint. Requires multicast reachability (Docker host/macvlan
// networking); on a bridge network it simply won't reach the LAN.
func Advertise(ctx context.Context, name string, port int, version string) error {
	cfg := dnssd.Config{
		Name: name,
		Type: emberServiceType,
		Port: port,
		Text: map[string]string{"version": version, "path": "/healthz"},
	}
	sv, err := dnssd.NewService(cfg)
	if err != nil {
		return err
	}
	rp, err := dnssd.NewResponder()
	if err != nil {
		return err
	}
	if _, err := rp.Add(sv); err != nil {
		return err
	}
	return rp.Respond(ctx) // blocks until ctx is done
}
