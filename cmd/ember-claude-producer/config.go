package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	defaultHeartbeatTTLHours = 6
	defaultHookTimeoutMs     = 500
	defaultDoneTTLSeconds    = 30
)

type Config struct {
	producer.Common
	producer.Gauges
	HeartbeatTTLHours     int
	HookTimeoutMs         int
	DoneTTLSeconds        int
	ActivityDetailEnabled bool
}

func (c Config) LogValue() slog.Value {
	attrs := append(c.Common.LogAttrs(), c.Gauges.LogAttrs()...)
	return slog.GroupValue(append(attrs,
		slog.Int("heartbeat_ttl_hours", c.HeartbeatTTLHours),
		slog.Int("hook_timeout_ms", c.HookTimeoutMs),
		slog.Int("done_ttl_seconds", c.DoneTTLSeconds),
		slog.Bool("activity_detail_enabled", c.ActivityDetailEnabled),
	)...)
}

func loadConfig() (Config, error) {
	cfg := Config{
		Common:                producer.DefaultCommon(),
		Gauges:                producer.DefaultGauges(),
		HeartbeatTTLHours:     defaultHeartbeatTTLHours,
		HookTimeoutMs:         defaultHookTimeoutMs,
		DoneTTLSeconds:        defaultDoneTTLSeconds,
		ActivityDetailEnabled: true,
	}
	path, err := envFilePath()
	if err != nil {
		return cfg, err
	}
	data, err := producer.ReadEnvFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring producer.env:", err)
	}
	for k, v := range data {
		if cfg.Common.Set(k, v) || cfg.Gauges.Set(k, v) {
			continue
		}
		switch k {
		case "EMBER_HEARTBEAT_TTL_HOURS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HeartbeatTTLHours = n
			}
		case "EMBER_HOOK_TIMEOUT_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HookTimeoutMs = n
			}
		case "EMBER_DONE_TTL_SECONDS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.DoneTTLSeconds = n
			}
		case "EMBER_ACTIVITY_DETAIL_ENABLED":
			cfg.ActivityDetailEnabled = producer.Bool(v, true)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		cfg.Common.Resolve(home)
	} else {
		cfg.Source = producer.ResolveSource(cfg.Source)
		if cfg.Token == "" {
			cfg.Token = os.Getenv("EMBER_TOKEN")
		}
	}
	return cfg, nil
}

func envFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return producer.EnvFilePath(home), nil
}
