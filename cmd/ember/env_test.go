package main

import "testing"

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
