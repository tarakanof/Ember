package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-interrupts
		goRuns.killAll()
		fmt.Fprintf(os.Stderr, "%v: killed the go run process groups\n", sig)
		os.Exit(1)
	}()
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
