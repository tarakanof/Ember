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
	// Sources is the set of session_meta.source kinds shown
	// (EMBER_CODEX_SOURCES); nil means defaultSources.
	Sources map[string]bool
	// IncludeClaude shows sessions Claude Code's Codex plugin starts
	// (EMBER_CODEX_INCLUDE_CLAUDE), marked "via Claude".
	IncludeClaude bool
	// AppServerEnabled observes TUI sessions through the Codex app-server
	// daemon socket when it exists (EMBER_CODEX_APPSERVER, default on).
	AppServerEnabled bool
	// AppServerSocket is $CODEX_HOME/app-server-control/app-server-control.sock.
	AppServerSocket string
	// CodexHome is $CODEX_HOME, else ~/.codex.
	CodexHome string
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
			// A LaunchAgent does not see a CODEX_HOME exported in the shell.
			if v != "" {
				envCodexHome = v
			}
		}
	}
	// CODEX_HOME: producer.env, else the process env, else ~/.codex. Both
	// the sessions dir and the daemon socket follow it.
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
