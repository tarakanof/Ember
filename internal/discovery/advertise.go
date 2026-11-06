package discovery

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/brutella/dnssd"
)

const (
	emberServiceType = "_ember._tcp"

	AdvertisedHostPrefix = "ember-"

	maxDNSLabel = 63
)

func PortFromAddr(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

func advertiseHost(machine string) string {
	label, _, _ := strings.Cut(machine, ".")
	host := AdvertisedHostPrefix + label
	if len(host) > maxDNSLabel {
		host = host[:maxDNSLabel]
	}
	return strings.TrimRight(host, "-")
}

func advertiseConfig(name string, port int, version, machine string) dnssd.Config {
	return dnssd.Config{
		Name: name,
		Type: emberServiceType,
		Host: advertiseHost(machine),
		Port: port,
		Text: map[string]string{"version": version, "path": "/healthz"},
	}
}

func Advertise(ctx context.Context, name string, port int, version string) error {
	machine, _ := os.Hostname()
	sv, err := dnssd.NewService(advertiseConfig(name, port, version, machine))
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
