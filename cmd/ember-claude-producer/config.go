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
	defaultHeartbeatTTLHours = 6
	defaultHookTimeoutMs     = 500
)

type Config struct {
	Source                string
	ServerURL             string
	Token                 string
	HeartbeatTTLHours     int
	HookTimeoutMs         int
	SourceColor           string
	ContextPctEnabled     bool
	ActivityDetailEnabled bool
	ActivityTrailEnabled  bool
	ContextNumberEnabled  bool
	RateBottomBarEnabled  bool
	RateResetEnabled      bool
}

// LogValue redacts the token. Implements slog.LogValuer.
func (c Config) LogValue() slog.Value {
	tokenStatus := "unset"
	if c.Token != "" {
		tokenStatus = "set"
	}
	return slog.GroupValue(
		slog.String("source", c.Source),
		slog.String("server_url", c.ServerURL),
		slog.String("token", tokenStatus),
		slog.Int("heartbeat_ttl_hours", c.HeartbeatTTLHours),
		slog.Int("hook_timeout_ms", c.HookTimeoutMs),
		slog.String("source_color", c.SourceColor),
		slog.Bool("context_pct_enabled", c.ContextPctEnabled),
		slog.Bool("activity_detail_enabled", c.ActivityDetailEnabled),
		slog.Bool("activity_trail_enabled", c.ActivityTrailEnabled),
		slog.Bool("context_number_enabled", c.ContextNumberEnabled),
		slog.Bool("rate_bottom_bar_enabled", c.RateBottomBarEnabled),
		slog.Bool("rate_reset_enabled", c.RateResetEnabled),
	)
}

func loadConfig() (Config, error) {
	cfg := Config{
		HeartbeatTTLHours:     defaultHeartbeatTTLHours,
		HookTimeoutMs:         defaultHookTimeoutMs,
		ContextPctEnabled:     true,
		ActivityDetailEnabled: true,
		ActivityTrailEnabled:  true,
	}
	path, err := envFilePath()
	if err != nil {
		return cfg, err
	}
	data, err := producer.ReadEnvFile(path)
	if err != nil {
		// Permission/symlink errors: log to stderr, treat as missing
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		switch k {
		case "EMBER_SOURCE":
			cfg.Source = v
		case "EMBER_SERVER_URL":
			cfg.ServerURL = v
		case "EMBER_TOKEN":
			cfg.Token = v
		case "EMBER_HEARTBEAT_TTL_HOURS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HeartbeatTTLHours = n
			}
		case "EMBER_HOOK_TIMEOUT_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HookTimeoutMs = n
			}
		case "EMBER_SOURCE_COLOR":
			cfg.SourceColor = v
		case "EMBER_CONTEXT_PCT_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ContextPctEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ContextPctEnabled = true
			}
		case "EMBER_ACTIVITY_DETAIL_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ActivityDetailEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ActivityDetailEnabled = true
			}
		case "EMBER_ACTIVITY_TRAIL_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ActivityTrailEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ActivityTrailEnabled = true
			}
		case "EMBER_CONTEXT_NUMBER_ENABLED":
			switch strings.ToLower(v) {
			case "true", "1", "yes", "on":
				cfg.ContextNumberEnabled = true
			}
		case "EMBER_RATE_BOTTOM_BAR":
			switch strings.ToLower(v) {
			case "true", "1", "yes", "on":
				cfg.RateBottomBarEnabled = true
			}
		case "EMBER_RATE_RESET":
			switch strings.ToLower(v) {
			case "true", "1", "yes", "on":
				cfg.RateResetEnabled = true
			}
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("EMBER_TOKEN")
	}
	return cfg, nil
}

func envFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ember", "producer.env"), nil
}
