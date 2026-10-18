package main

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Unsetenv("EMBER_CLOCK"); err != nil {
		fmt.Fprintln(os.Stderr, "unset EMBER_CLOCK:", err)
		os.Exit(2)
	}
	guard, tr, rt, err := installLANGuard(http.DefaultTransport)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install LAN guard:", err)
		os.Exit(2)
	}
	testLANGuard, guardedTransport, http.DefaultTransport = guard, tr, rt
	code := m.Run()
	if guard.report(os.Stderr) && code == 0 {
		code = 1
	}
	os.Exit(code)
}

func TestEnvEnabled(t *testing.T) {
	cases := map[string]bool{
		"": true, "1": true, "true": true, "TRUE": true, "yes": true,
		"0": false, "false": false, "no": false, "off": false, " off ": false, "OFF": false,
	}
	for in, want := range cases {
		if got := envEnabled(in); got != want {
			t.Errorf("envEnabled(%q) = %v, want %v", in, got, want)
		}
	}
}
