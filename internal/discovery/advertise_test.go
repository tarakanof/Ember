package discovery

import (
	"strings"
	"testing"

	"github.com/brutella/dnssd"
)

func TestPortFromAddr(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want int
	}{
		{":3627", 3627},
		{"0.0.0.0:9000", 9000},
		{"127.0.0.1:80", 80},
	} {
		if p, err := PortFromAddr(tc.addr); err != nil || p != tc.want {
			t.Fatalf("PortFromAddr(%q)=%d,%v want %d", tc.addr, p, err, tc.want)
		}
	}
	if _, err := PortFromAddr("not-an-addr"); err == nil {
		t.Fatal("expected error for malformed addr")
	}
}

func TestAdvertiseHostNeverClaimsTheMachineName(t *testing.T) {
	for _, tc := range []struct{ machine, want string }{
		{"Tosaka", "ember-Tosaka"},
		{"Tosaka.local", "ember-Tosaka"},
		{"nas.home.arpa", "ember-nas"},
		{"ember", "ember-ember"},
		{"", "ember"},
		{".", "ember"},
		{strings.Repeat("a", 57), "ember-" + strings.Repeat("a", 57)},
		{strings.Repeat("a", 58), "ember-" + strings.Repeat("a", 57)},
		{strings.Repeat("a", 56) + "-b", "ember-" + strings.Repeat("a", 56)},
	} {
		if got := advertiseHost(tc.machine); got != tc.want {
			t.Errorf("advertiseHost(%q) = %q, want %q", tc.machine, got, tc.want)
		}
	}
}

func TestAdvertiseServicePublishesItsOwnSRVTarget(t *testing.T) {
	sv, err := dnssd.NewService(advertiseConfig("Ember", 3627, "abc", "Tosaka"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sv.Hostname(), "ember-Tosaka.local."; got != want {
		t.Fatalf("SRV target = %q, want %q", got, want)
	}
	if sv.Name != "Ember" || sv.Type != emberServiceType || sv.Port != 3627 {
		t.Fatalf("service = %+v", sv)
	}
	if sv.Text["version"] != "abc" || sv.Text["path"] != "/healthz" {
		t.Fatalf("TXT = %+v", sv.Text)
	}
}
