package main

import "strings"

// envEnabled reads a default-on boolean env toggle (EMBER_MDNS_ADVERTISE,
// EMBER_FIRMWARE_CHECK): empty or unset means enabled; "0", "false", "no" and
// "off" (any case, surrounding space ignored) disable it.
func envEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
