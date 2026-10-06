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
	producer.Common
	producer.Gauges
	RatePctEnabled        bool
	PollIntervalMs        int
	ActivityWindowSeconds int
	SessionsDir           string
	StateDir              string
	Sources               map[string]bool
	IncludeClaude         bool
	AppServerEnabled      bool
	AppServerSocket       string
	CodexHome             string
}

var defaultSources = map[string]bool{"cli": true, "vscode": true}

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

func (c Config) LogValue() slog.Value {
	attrs := append(c.Common.LogAttrs(), c.Gauges.LogAttrs()...)
	return slog.GroupValue(append(attrs,
		slog.Bool("rate_pct_enabled", c.RatePctEnabled),
		slog.Int("poll_interval_ms", c.PollIntervalMs),
		slog.Int("activity_window_seconds", c.ActivityWindowSeconds),
		slog.String("sessions_dir", c.SessionsDir),
		slog.String("state_dir", c.StateDir),
		slog.String("sources", sourceList(c.Sources)),
		slog.Bool("include_claude", c.IncludeClaude),
		slog.Bool("appserver_enabled", c.AppServerEnabled),
		slog.String("appserver_socket", c.AppServerSocket),
	)...)
}

func loadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Common:                producer.DefaultCommon(),
		Gauges:                producer.DefaultGauges(),
		RatePctEnabled:        true,
		AppServerEnabled:      true,
		CodexHome:             filepath.Join(home, ".codex"),
		PollIntervalMs:        defaultPollIntervalMs,
		ActivityWindowSeconds: defaultActivityWindowSeconds,
		StateDir:              filepath.Join(home, ".local", "state", "ember", "sessions"),
	}
	var sessionsDir, envCodexHome string
	data, err := producer.ReadEnvFile(producer.EnvFilePath(home))
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		if cfg.Common.Set(k, v) || cfg.Gauges.Set(k, v) {
			continue
		}
		switch k {
		case "EMBER_RATE_PCT_ENABLED":
			cfg.RatePctEnabled = producer.Bool(v, true)
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
			cfg.IncludeClaude = producer.Bool(v, false)
		case "EMBER_CODEX_APPSERVER":
			cfg.AppServerEnabled = producer.Bool(v, true)
		case "EMBER_CODEX_SESSIONS_DIR":
			if v != "" {
				sessionsDir = v
			}
		case "CODEX_HOME":
			if v != "" {
				envCodexHome = v
			}
		}
	}
	if envCodexHome != "" {
		cfg.CodexHome = envCodexHome
	} else if ch := os.Getenv("CODEX_HOME"); ch != "" {
		cfg.CodexHome = ch
	}
	cfg.SessionsDir = filepath.Join(cfg.CodexHome, "sessions")
	if sessionsDir != "" {
		cfg.SessionsDir = sessionsDir
	}
	cfg.AppServerSocket = appServerSocket(cfg.CodexHome)
	cfg.Common.Resolve(home)
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}
