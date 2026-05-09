package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
)

// Config-load sentinel errors. Wrap with fmt.Errorf("...: %w", ErrConfig*).
var (
	ErrConfigRead     = errors.New("config read")
	ErrConfigParse    = errors.New("config parse")
	ErrConfigValidate = errors.New("config validate")
)

// resolveConfigPath picks the config path the same way the running server does.
// Precedence: -config flag value → CONFIG_PATH env → ./config.json (if it exists) → defaults-only ("").
// The returned source string ∈ {"flag","env","cwd","defaults"} describes which arm matched.
func resolveConfigPath(flagValue string) (path, source string) {
	if flagValue != "" {
		return flagValue, "flag"
	}
	if env := os.Getenv("CONFIG_PATH"); env != "" {
		return env, "env"
	}
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json", "cwd"
	}
	return "", "defaults"
}

// parseConfigFile reads + decodes a config file. Returns:
//   - ErrConfigRead-wrapped error if the file can't be read.
//   - ErrConfigParse-wrapped error if the JSON is malformed (including unknown fields).
//   - the parsed Config (without applyDefaults; the caller chooses when to apply).
func parseConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %s: %v", ErrConfigRead, path, err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %s: %v", ErrConfigParse, path, err)
	}
	return cfg, nil
}

// validateConfig enforces required fields and well-formedness. Returns
// ErrConfigValidate-wrapped on failure. Run AFTER applyDefaults so empty
// optional fields don't trigger.
func validateConfig(cfg Config) error {
	if cfg.AWTRIX.HTTPBaseURL == "" {
		return fmt.Errorf("%w: awtrix.http_base_url is required", ErrConfigValidate)
	}
	if _, err := url.ParseRequestURI(cfg.AWTRIX.HTTPBaseURL); err != nil {
		return fmt.Errorf("%w: awtrix.http_base_url %q: %v", ErrConfigValidate, cfg.AWTRIX.HTTPBaseURL, err)
	}
	if cfg.AWTRIX.TimeoutSeconds <= 0 {
		return fmt.Errorf("%w: awtrix.timeout_seconds must be > 0", ErrConfigValidate)
	}
	return nil
}
