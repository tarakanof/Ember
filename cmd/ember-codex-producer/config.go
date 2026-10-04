package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	defaultPollIntervalMs        = 2000
	minPollIntervalMs            = 250
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
	// Sources is the set of session_meta.source kinds shown
	// (EMBER_CODEX_SOURCES); nil means defaultSources.
	Sources map[string]bool
	// IncludeClaude shows sessions Claude Code's Codex plugin starts
	// (EMBER_CODEX_INCLUDE_CLAUDE), marked "via Claude".
	IncludeClaude bool
}

// defaultSources are the interactive Codex front ends: the TUI and the
// IDE/desktop app. exec and mcp are driven by scripts or other agents.
var defaultSources = map[string]bool{"cli": true, "vscode": true}

// tracks reports whether a session with this source and originator is shown.
func (c Config) tracks(meta sessionMeta) bool {
	if meta.originator == claudeOriginator && !c.IncludeClaude {
		return false
	}
	set := c.Sources
	if set == nil {
		set = defaultSources
	}
	return set[meta.source]
}

// parseSources reads a comma-separated source list; empty yields nil (the default).
func parseSources(v string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.Split(v, ",") {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			out[f] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sourceList(set map[string]bool) string {
	if set == nil {
		set = defaultSources
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
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
		slog.String("sources", sourceList(c.Sources)),
		slog.Bool("include_claude", c.IncludeClaude),
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
		case "EMBER_CODEX_SOURCES":
			cfg.Sources = parseSources(v)
		case "EMBER_CODEX_INCLUDE_CLAUDE":
			switch strings.ToLower(v) {
			case "true", "1", "yes", "on":
				cfg.IncludeClaude = true
			}
		case "EMBER_CODEX_SESSIONS_DIR":
			if v != "" {
				cfg.SessionsDir = v
			}
		}
	}
	cfg.Source = producer.ResolveSource(cfg.Source)
	if cfg.Token == "" {
		cfg.Token = os.Getenv("EMBER_TOKEN")
	}
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}
