package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_TokenEnvFallback(t *testing.T) {
	t.Setenv("STATUS_TOKEN", "from-env")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "from-env" {
		t.Errorf("Token = %q, want from-env", cfg.Token)
	}
}

func TestLoadConfig_DefaultTimings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HeartbeatTTLHours != 6 {
		t.Errorf("HeartbeatTTLHours = %d, want 6", cfg.HeartbeatTTLHours)
	}
	if cfg.HookTimeoutMs != 500 {
		t.Errorf("HookTimeoutMs = %d, want 500", cfg.HookTimeoutMs)
	}
}

func TestLoadConfig_G3Fields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	envDir := filepath.Join(dir, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "STATUS_SOURCE_COLOR=#aa66ff\nSTATUS_CONTEXT_PCT_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceColor != "#aa66ff" {
		t.Errorf("SourceColor = %q, want #aa66ff", cfg.SourceColor)
	}
	if cfg.ContextPctEnabled != false {
		t.Errorf("ContextPctEnabled = %v, want false", cfg.ContextPctEnabled)
	}
}

func TestLoadConfig_ContextPctEnabled_DefaultTrue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	envDir := filepath.Join(dir, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "STATUS_SOURCE=x\n"
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextPctEnabled != true {
		t.Errorf("ContextPctEnabled = %v, want true", cfg.ContextPctEnabled)
	}
}

func TestLoadConfig_ContextWindowTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	envDir := filepath.Join(dir, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "STATUS_CONTEXT_WINDOW_TOKENS=1000000\n"
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextWindowTokens != 1_000_000 {
		t.Errorf("ContextWindowTokens = %d, want 1000000", cfg.ContextWindowTokens)
	}
}

func TestLoadConfig_ContextWindowTokens_DefaultZero(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	envDir := filepath.Join(dir, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte("STATUS_SOURCE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextWindowTokens != 0 {
		t.Errorf("ContextWindowTokens = %d, want 0 (unset → model default applies downstream)", cfg.ContextWindowTokens)
	}
}

func TestLoadConfig_ActivityDetailDefaultsTrue(t *testing.T) {
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
	if !cfg.ActivityDetailEnabled {
		t.Error("ActivityDetailEnabled default = false, want true")
	}
}

func TestLoadConfig_ActivityDetailDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("STATUS_SOURCE=mbp\nSTATUS_ACTIVITY_DETAIL_ENABLED=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivityDetailEnabled {
		t.Error("ActivityDetailEnabled = true with =false set, want false")
	}
}

func TestLoadConfig_ActivityTrailDefaultsTrue(t *testing.T) {
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

func TestLoadConfig_ActivityTrailDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("STATUS_SOURCE=mbp\nSTATUS_ACTIVITY_TRAIL_ENABLED=off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivityTrailEnabled {
		t.Error("ActivityTrailEnabled = true with =off set, want false")
	}
}
