package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	toolName                     = "t3"
	defaultPollIntervalMs        = 2000
	minPollIntervalMs            = 250
	defaultActivityWindowSeconds = 300
)

type Config struct {
	Source                string
	ServerURL             string // effective URL: explicit, else the cached discovery
	ServerConfigured      string // EMBER_SERVER_URL as written
	ServerAuto            bool   // EMBER_SERVER_URL empty or "auto": discover over mDNS
	ServerInstance        string // EMBER_SERVER_INSTANCE: which server when several answer
	Token                 string
	SourceColor           string
	ActivityTrailEnabled  bool
	SourceCardEnabled     bool
	SessionBarEnabled     bool
	PollIntervalMs        int
	ActivityWindowSeconds int
	T3Home                string
	StateDir              string
}

// LogValue redacts the token.
func (c Config) LogValue() slog.Value {
	tok := "unset"
	if c.Token != "" {
		tok = "set"
	}
	return slog.GroupValue(
		slog.String("source", c.Source),
		slog.String("server_url", c.ServerURL),
		slog.Bool("server_auto", c.ServerAuto),
		slog.String("token", tok),
		slog.String("source_color", c.SourceColor),
		slog.Bool("activity_trail_enabled", c.ActivityTrailEnabled),
		slog.Bool("source_card_enabled", c.SourceCardEnabled),
		slog.Bool("session_bar_enabled", c.SessionBarEnabled),
		slog.Int("poll_interval_ms", c.PollIntervalMs),
		slog.Int("activity_window_seconds", c.ActivityWindowSeconds),
		slog.String("t3_home", c.T3Home),
		slog.String("state_dir", c.StateDir),
	)
}

// loadConfig reads ~/.config/ember/producer.env (shared with the other
// producers). EMBER_T3_HOME mirrors T3's own T3CODE_HOME; T3CODE_HOME itself
// is not read because a LaunchAgent does not inherit the user's shell env.
func loadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ActivityTrailEnabled:  true,
		SourceCardEnabled:     true,
		SessionBarEnabled:     true,
		PollIntervalMs:        defaultPollIntervalMs,
		ActivityWindowSeconds: defaultActivityWindowSeconds,
		T3Home:                filepath.Join(home, ".t3"),
		StateDir:              filepath.Join(home, ".local", "state", "ember", "sessions"),
	}
	data, err := producer.ReadEnvFile(filepath.Join(home, ".config", "ember", "producer.env"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		switch k {
		case "EMBER_SOURCE":
			cfg.Source = v
		case "EMBER_SERVER_URL":
			cfg.ServerURL = v
		case "EMBER_SERVER_INSTANCE":
			cfg.ServerInstance = v
		case "EMBER_TOKEN":
			cfg.Token = v
		case "EMBER_SOURCE_COLOR":
			cfg.SourceColor = v
		case "EMBER_ACTIVITY_TRAIL_ENABLED":
			cfg.ActivityTrailEnabled = parseBoolDefault(v, cfg.ActivityTrailEnabled)
		case "EMBER_SOURCE_CARD":
			cfg.SourceCardEnabled = parseBoolDefault(v, cfg.SourceCardEnabled)
		case "EMBER_SESSION_BAR":
			cfg.SessionBarEnabled = parseBoolDefault(v, cfg.SessionBarEnabled)
		case "EMBER_T3_POLL_INTERVAL_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.PollIntervalMs = n
			}
		case "EMBER_T3_ACTIVITY_WINDOW_SECONDS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.ActivityWindowSeconds = n
			}
		case "EMBER_T3_HOME":
			if strings.HasPrefix(v, "~/") {
				v = filepath.Join(home, v[2:])
			}
			if v != "" {
				cfg.T3Home = v
			}
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("EMBER_TOKEN")
	}
	cfg.Source = producer.ResolveSource(cfg.Source)
	cfg.ServerConfigured = cfg.ServerURL
	cfg.ServerURL, cfg.ServerAuto = producer.ResolveServerURL(home, cfg.ServerURL, cfg.ServerInstance)
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}

// parseBoolDefault follows the other producers: an empty value means on,
// an unrecognised one keeps the default.
func parseBoolDefault(v string, def bool) bool {
	switch strings.ToLower(v) {
	case "false", "0", "no", "off":
		return false
	case "true", "1", "yes", "on", "":
		return true
	}
	return def
}
