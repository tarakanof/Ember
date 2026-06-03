package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

// redactConfig returns a copy of c with secrets stripped.
//   - Auth.StatusToken: "<redacted>" if non-empty (so reader knows it was set), "" otherwise.
//   - AWTRIX.HTTPBaseURL: userinfo (e.g. http://user:pass@host) stripped via url.Parse.
func redactConfig(c Config) Config {
	if c.Auth.StatusToken != "" {
		c.Auth.StatusToken = "<redacted>"
	}
	if u, err := url.Parse(c.AWTRIX.HTTPBaseURL); err == nil && u.User != nil {
		u.User = nil
		c.AWTRIX.HTTPBaseURL = u.String()
	}
	return c
}

// runPrintConfig loads + applyDefaults + validates the config at path,
// redacts secrets, prints JSON to stdout. Exits the process on any error.
func runPrintConfig(path string) {
	resolved, _ := resolveConfigPath(path)
	if resolved == "" {
		fmt.Fprintln(os.Stderr, "print-config: no config source resolved (no -config flag, no CONFIG_PATH env, no ./config.json)")
		os.Exit(1)
	}
	cfg, err := parseConfigFile(resolved)
	if err != nil {
		fmt.Fprintln(os.Stderr, "print-config:", err)
		os.Exit(1)
	}
	cfg.applyDefaults()
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "print-config:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(redactConfig(cfg)); err != nil {
		fmt.Fprintln(os.Stderr, "print-config: encode:", err)
		os.Exit(1)
	}
}
