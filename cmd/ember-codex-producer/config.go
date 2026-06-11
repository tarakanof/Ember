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
	defaultPollIntervalMs = 2000
	minPollIntervalMs     = 250
	// A codex session has no "window closed" signal (the daemon only tails
	// rollout files), so it idles after this much rollout inactivity. 300s keeps
	// it shown through long thinking/between-turn gaps — parity with the Claude
	// producer staying present for the session — while clearing a finished
	// session within ~5 min. Override with EMBER_CODEX_ACTIVITY_WINDOW_SECONDS.
	defaultActivityWindowSeconds = 300
)

type Config struct {
	Source                string
	ServerURL             string
	Token                 string
	SourceColor           string
	ContextPctEnabled     bool
	RatePctEnabled        bool
	ActivityTrailEnabled  bool
	ContextNumberEnabled  bool
	RateBottomBarEnabled  bool
	RateResetEnabled      bool
	SourceCardEnabled     bool
	SessionBarEnabled     bool
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
		slog.Bool("context_number_enabled", c.ContextNumberEnabled),
		slog.Bool("rate_bottom_bar_enabled", c.RateBottomBarEnabled),
		slog.Bool("rate_reset_enabled", c.RateResetEnabled),
		slog.Bool("source_card_enabled", c.SourceCardEnabled),
		slog.Bool("session_bar_enabled", c.SessionBarEnabled),
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
		SourceCardEnabled:     true,
		SessionBarEnabled:     true,
		PollIntervalMs:        defaultPollIntervalMs,
		ActivityWindowSeconds: defaultActivityWindowSeconds,
		SessionsDir:           filepath.Join(home, ".codex", "sessions"),
		StateDir:              filepath.Join(home, ".local", "state", "ember", "sessions"),
	}
	envPath := filepath.Join(home, ".config", "ember", "producer.env")
	data, err := producer.ReadEnvFile(envPath)
	if err != nil {
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
		case "EMBER_SOURCE_COLOR":
			cfg.SourceColor = v
		case "EMBER_CONTEXT_PCT_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.ContextPctEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.ContextPctEnabled = true
			}
		case "EMBER_RATE_PCT_ENABLED":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.RatePctEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.RatePctEnabled = true
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
		case "EMBER_SOURCE_CARD":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.SourceCardEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.SourceCardEnabled = true
			}
		case "EMBER_SESSION_BAR":
			switch strings.ToLower(v) {
			case "false", "0", "no", "off":
				cfg.SessionBarEnabled = false
			case "true", "1", "yes", "on", "":
				cfg.SessionBarEnabled = true
			}
		case "EMBER_CODEX_POLL_INTERVAL_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.PollIntervalMs = n
			}
		case "EMBER_CODEX_ACTIVITY_WINDOW_SECONDS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.ActivityWindowSeconds = n
			}
		case "EMBER_CODEX_SESSIONS_DIR":
			if v != "" {
				cfg.SessionsDir = v
			}
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("EMBER_TOKEN")
	}
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}
