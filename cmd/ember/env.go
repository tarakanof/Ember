package main

import (
	"os"
	"strings"
)

// clockDisabled reports EMBER_CLOCK=off (also 0/false/no/disabled): the server
// then does no clock I/O at all (no AWTRIX HTTP, mDNS browse, UDP find,
// rediscovery or probe), so a scratch server cannot reach a real clock.
func clockDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EMBER_CLOCK"))) {
	case "off", "0", "false", "no", "disabled":
		return true
	}
	return false
}

func envEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// envOptIn reports whether an off-by-default toggle is explicitly on.
func envOptIn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
