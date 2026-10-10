package main

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Unsetenv("EMBER_CLOCK"); err != nil {
		fmt.Fprintln(os.Stderr, "unset EMBER_CLOCK:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
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
