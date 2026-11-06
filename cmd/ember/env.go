package main

import (
	"os"
	"strings"
)

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

func envOptIn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
