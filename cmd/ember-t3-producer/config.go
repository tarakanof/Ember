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
	producer.Common
	PollIntervalMs        int
	ActivityWindowSeconds int
	T3Home                string
	StateDir              string
}

// LogValue redacts the token.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(append(c.Common.LogAttrs(),
		slog.Int("poll_interval_ms", c.PollIntervalMs),
		slog.Int("activity_window_seconds", c.ActivityWindowSeconds),
		slog.String("t3_home", c.T3Home),
		slog.String("state_dir", c.StateDir),
	)...)
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
		Common:                producer.DefaultCommon(),
		PollIntervalMs:        defaultPollIntervalMs,
		ActivityWindowSeconds: defaultActivityWindowSeconds,
		T3Home:                filepath.Join(home, ".t3"),
		StateDir:              filepath.Join(home, ".local", "state", "ember", "sessions"),
	}
	data, err := producer.ReadEnvFile(producer.EnvFilePath(home))
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		if cfg.Common.Set(k, v) {
			continue
		}
		switch k {
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
	cfg.Common.Resolve(home)
	if cfg.PollIntervalMs < minPollIntervalMs {
		cfg.PollIntervalMs = minPollIntervalMs
	}
	return cfg, nil
}
