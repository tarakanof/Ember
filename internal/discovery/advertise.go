package discovery

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/brutella/dnssd"
)

const (
	emberServiceType = "_ember._tcp"

	AdvertisedHostPrefix = "ember-"

	InstanceBase = "Ember"

	maxDNSLabel = 63
)

var machineNameFuncs = machineNameFuncsFor(runtime.GOOS)

func machineNameFuncsFor(goos string) []func() (string, error) {
	if goos != "darwin" {
		return []func() (string, error){os.Hostname}
	}
	return []func() (string, error){
		func() (string, error) {
			out, err := exec.Command("scutil", "--get", "LocalHostName").Output()
			return strings.TrimSpace(string(out)), err
		},
		os.Hostname,
	}
}

func machineName(funcs []func() (string, error)) string {
	for _, f := range funcs {
		if h, err := f(); err == nil && strings.TrimSpace(h) != "" {
			return strings.TrimSpace(h)
		}
	}
	return ""
}

func advertiseName(machine string) string {
	label, _, _ := strings.Cut(machine, ".")
	if label == "" {
		return InstanceBase
	}
	if room := maxDNSLabel - len(InstanceBase) - len(" ()"); len(label) > room {
		label = label[:room]
	}
	return InstanceBase + " (" + label + ")"
}

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

func Advertise(ctx context.Context, port int, version string) error {
	machine := machineName(machineNameFuncs)
	sv, err := dnssd.NewService(advertiseConfig(advertiseName(machine), port, version, machine))
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
