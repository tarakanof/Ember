package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

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

func runPrintConfig(path string, env serverEnv) {
	for _, line := range env.describe() {
		fmt.Fprintln(os.Stderr, "print-config:", line)
	}
	resolved, _ := resolveConfigPath(path)
	if resolved == "" {
		fmt.Fprintln(os.Stderr, "print-config: no config source resolved (no -config flag, no CONFIG_PATH env, no $EMBER_DATA_DIR/config.json, no ./config.json)")
		os.Exit(1)
	}
	cfg, err := parseConfigFile(resolved)
	if err != nil {
		fmt.Fprintln(os.Stderr, "print-config:", err)
		os.Exit(1)
	}
	if userClockUnset(env.User, cfg.AWTRIX.HTTPBaseURL) {
		fmt.Fprintln(os.Stderr, "print-config: no clock configured under -user; the http_base_url below is a placeholder the server does not use")
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
