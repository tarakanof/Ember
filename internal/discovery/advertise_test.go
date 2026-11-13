package discovery

import (
	"errors"
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
	sv, err := dnssd.NewService(advertiseConfig(advertiseName("Tosaka"), 3627, "abc", "Tosaka"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sv.Hostname(), "ember-Tosaka.local."; got != want {
		t.Fatalf("SRV target = %q, want %q", got, want)
	}
	if sv.Name != "Ember (Tosaka)" || sv.Type != emberServiceType || sv.Port != 3627 {
		t.Fatalf("service = %+v", sv)
	}
	if sv.Text["version"] != "abc" || sv.Text["path"] != "/healthz" {
		t.Fatalf("TXT = %+v", sv.Text)
	}
}

func TestAdvertiseNameCarriesTheMachineLabel(t *testing.T) {
	for _, tc := range []struct{ machine, want string }{
		{"Tosaka", "Ember (Tosaka)"},
		{"Tosaka.local", "Ember (Tosaka)"},
		{"nas.home.arpa", "Ember (nas)"},
		{"Mac", "Ember (Mac)"},
		{"", "Ember"},
		{".", "Ember"},
		{strings.Repeat("a", 55), "Ember (" + strings.Repeat("a", 55) + ")"},
		{strings.Repeat("a", 80), "Ember (" + strings.Repeat("a", 55) + ")"},
	} {
		got := advertiseName(tc.machine)
		if got != tc.want || len(got) > maxDNSLabel {
			t.Errorf("advertiseName(%q) = %q, want %q", tc.machine, got, tc.want)
		}
	}
}

func TestMachineNameFallsBackWhenTheFirstSourceFails(t *testing.T) {
	fail := func() (string, error) { return "", errors.New("no scutil") }
	blank := func() (string, error) { return " \n", nil }
	ok := func() (string, error) { return "studio\n", nil }
	if got := machineName([]func() (string, error){fail, blank, ok}); got != "studio" {
		t.Fatalf("machineName = %q, want studio", got)
	}
	if got := machineName([]func() (string, error){fail}); got != "" {
		t.Fatalf("machineName with no source = %q, want empty", got)
	}
	if n := len(machineNameFuncsFor("linux")); n != 1 {
		t.Fatalf("linux sources = %d, want 1", n)
	}
	if n := len(machineNameFuncsFor("darwin")); n != 2 {
		t.Fatalf("darwin sources = %d, want scutil then os.Hostname", n)
	}
}
