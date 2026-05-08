package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	defaultHeartbeatTTLHours = 6
	defaultHookTimeoutMs     = 500
)

type Config struct {
	Source            string
	ServerURL         string
	Token             string
	HeartbeatTTLHours int
	HookTimeoutMs     int
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
	)
}

func loadConfig() (Config, error) {
	cfg := Config{
		HeartbeatTTLHours: defaultHeartbeatTTLHours,
		HookTimeoutMs:     defaultHookTimeoutMs,
	}
	path, err := envFilePath()
	if err != nil {
		return cfg, err
	}
	data, err := readEnvFile(path)
	if err != nil {
		// Permission/symlink errors: log to stderr, treat as missing
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
		case "STATUS_HEARTBEAT_TTL_HOURS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HeartbeatTTLHours = n
			}
		case "STATUS_HOOK_TIMEOUT_MS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.HookTimeoutMs = n
			}
		}
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("STATUS_TOKEN")
	}
	return cfg, nil
}

func envFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "awtrix-ai-status", "producer.env"), nil
}

func readEnvFile(path string) (map[string]string, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if stat.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%s permission must be 0600 (got %#o)", path, stat.Mode().Perm())
	}
	if sysStat, ok := stat.Sys().(*syscall.Stat_t); ok {
		if int(sysStat.Uid) != os.Geteuid() {
			return nil, fmt.Errorf("%s must be owned by current user", path)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out, s.Err()
}
