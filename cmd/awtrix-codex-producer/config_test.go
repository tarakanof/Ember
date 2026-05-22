package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	envDir := filepath.Join(dir, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadConfig_Defaults(t *testing.T) {
	home := writeEnv(t, "STATUS_SOURCE=mbp\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalMs != 2000 {
		t.Errorf("PollIntervalMs = %d, want 2000", cfg.PollIntervalMs)
	}
	if cfg.ActivityWindowSeconds != 90 {
		t.Errorf("ActivityWindowSeconds = %d, want 90", cfg.ActivityWindowSeconds)
	}
	if !cfg.ContextPctEnabled {
		t.Error("ContextPctEnabled should default true")
	}
	want := filepath.Join(home, ".codex", "sessions")
	if cfg.SessionsDir != want {
		t.Errorf("SessionsDir = %q, want %q", cfg.SessionsDir, want)
	}
	wantState := filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions")
	if cfg.StateDir != wantState {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, wantState)
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	writeEnv(t, "STATUS_SOURCE=mbp\nSTATUS_CODEX_POLL_INTERVAL_MS=500\nSTATUS_CODEX_ACTIVITY_WINDOW_SECONDS=30\nSTATUS_CODEX_SESSIONS_DIR=/tmp/sess\nSTATUS_SOURCE_COLOR=#aa66ff\nSTATUS_CONTEXT_PCT_ENABLED=false\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalMs != 500 || cfg.ActivityWindowSeconds != 30 || cfg.SessionsDir != "/tmp/sess" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.SourceColor != "#aa66ff" || cfg.ContextPctEnabled {
		t.Errorf("color/ctx flags wrong: %+v", cfg)
	}
}

func TestLoadConfig_PollIntervalFloor(t *testing.T) {
	writeEnv(t, "STATUS_SOURCE=mbp\nSTATUS_CODEX_POLL_INTERVAL_MS=10\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalMs != 250 {
		t.Errorf("PollIntervalMs = %d, want clamped to 250", cfg.PollIntervalMs)
	}
}

func TestLoadConfig_RatePctEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no producer.env → defaults
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RatePctEnabled {
		t.Errorf("default RatePctEnabled = false, want true")
	}
}

func TestLoadConfig_CodexActivityTrailDefaultsTrue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("STATUS_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ActivityTrailEnabled {
		t.Error("ActivityTrailEnabled default = false, want true")
	}
}

func TestLoadConfig_CodexActivityTrailDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("STATUS_SOURCE=mbp\nSTATUS_ACTIVITY_TRAIL_ENABLED=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivityTrailEnabled {
		t.Error("ActivityTrailEnabled = true with =0 set, want false")
	}
}
