package discovery

import (
	"net"
	"testing"

	"github.com/brutella/dnssd"
)

func TestEmberServerFromPrefersIPv4(t *testing.T) {
	e := dnssd.BrowseEntry{
		Name: "Ember", Host: "unraid.local.", Port: 3627,
		IPs:  []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.0.36")},
		Text: map[string]string{"version": "1.4.0", "path": "/healthz"},
	}
	got, ok := emberServerFrom(e)
	want := EmberServer{Name: "Ember", Host: "unraid.local.", URL: "http://192.168.0.36:3627", Version: "1.4.0"}
	if !ok || got != want {
		t.Fatalf("got %+v, %v", got, ok)
	}
}

func TestEmberServerFromSkipsUnresolved(t *testing.T) {
	if _, ok := emberServerFrom(dnssd.BrowseEntry{Name: "Ember", Port: 3627}); ok {
		t.Error("entry without IPs accepted")
	}
	if _, ok := emberServerFrom(dnssd.BrowseEntry{Name: "Ember", IPs: []net.IP{net.ParseIP("10.0.0.1")}}); ok {
		t.Error("entry without port accepted")
	}
}
