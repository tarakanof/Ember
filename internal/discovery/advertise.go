package discovery

import (
	"context"
	"net"
	"strconv"

	"github.com/brutella/dnssd"
)

const emberServiceType = "_ember._tcp"

// PortFromAddr extracts the numeric port from a listen address like ":3627" or
// "0.0.0.0:9000".
func PortFromAddr(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// Advertise announces an _ember._tcp service until ctx is cancelled.
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
	return rp.Respond(ctx)
}
