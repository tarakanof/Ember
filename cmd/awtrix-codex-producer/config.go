package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dt/awtrix-ai-status/internal/producer"
)

const (
	defaultPollIntervalMs        = 2000
	minPollIntervalMs            = 250
	defaultActivityWindowSeconds = 90
)

type Config struct {
	Source                string
	ServerURL             string
	Token                 string
	SourceColor           string
	ContextPctEnabled     bool
	RatePctEnabled        bool
	ActivityTrailEnabled  bool
	PollIntervalMs        int
	ActivityWindowSeconds int
	SessionsDir           string
	StateDir              string
}

// LogValue redacts the token. Implements slog.LogValuer.
func (c Config) LogValue() slog.Value {
	tok := "unset"
	if c.Token != "" {
		tok = "set"
	}
	return slog.GroupValue(
		slog.String("source", c.Source),
		slog.String("server_url", c.ServerURL),
		slog.String("token", tok),
		slog.String("source_color", c.SourceColor),
		slog.Bool("context_pct_enabled", c.ContextPctEnabled),
		slog.Bool("rate_pct_enabled", c.RatePctEnabled),
		slog.Bool("activity_trail_enabled", c.ActivityTrailEnabled),
		slog.Int("poll_interval_ms", c.PollIntervalMs),
		slog.Int("activity_window_seconds", c.ActivityWindowSeconds),
		slog.String("sessions_dir", c.SessionsDir),
		slog.String("state_dir", c.StateDir),
	)
}

func loadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ContextPctEnabled:     true,
		RatePctEnabled:        true,
		ActivityTrailEnabled:  true,
		PollIntervalMs:        defaultPollIntervalMs,
		ActivityWindowSeconds: defaultActivityWindowSeconds,
		SessionsDir:           filepath.Join(home, ".codex", "sessions"),
		StateDir:              filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions"),
	}
	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
	data, err := producer.ReadEnvFile(envPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		switch k {
		case "STATUS_SOURCE":
			cfg.Source = v
		case "STATUS_SERVER_URL":
			cfg.ServerURL = v
		case "STATUS_TOKEN":
			cfg.Token = v
		case "STATUS_SOURCE_COLOR":
			cfg.SourceColor = v
		case "STATUS_CONTEXT_PCT_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ContextPctEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ContextPctEnabled = true
			}
		case "STATUS_RATE_PCT_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.RatePctEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.RatePctEnabled = true
			}
		case "STATUS_ACTIVITY_TRAIL_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ActivityTrailEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ActivityTrailEnabled = true
			}
		case "STATUS_CODEX_POLL_INTERVAL_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.PollIntervalMs = n
			}
		case "STATUS_CODEX_ACTIVITY_WINDOW_SECONDS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.ActivityWindowSeconds = n
			}
		case "STATUS_CODEX_SESSIONS_DIR":
			if v != "" {
				cfg.SessionsDir = v
			}
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("STATUS_TOKEN")
	}
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}
