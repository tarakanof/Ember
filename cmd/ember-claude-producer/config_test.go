package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_TokenEnvFallback(t *testing.T) {
	t.Setenv("EMBER_TOKEN", "from-env")
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
	envDir := filepath.Join(dir, ".config", "ember")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "EMBER_SOURCE_COLOR=#aa66ff\nEMBER_CONTEXT_PCT_ENABLED=false\n"
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
	envDir := filepath.Join(dir, ".config", "ember")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "EMBER_SOURCE=x\n"
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

func TestLoadConfig_ActivityDetailDefaultsTrue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
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
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_ACTIVITY_DETAIL_ENABLED=false\n"), 0o600); err != nil {
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
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
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
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_ACTIVITY_TRAIL_ENABLED=off\n"), 0o600); err != nil {
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

func TestLoadConfig_ContextNumberDefaultsFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.ContextNumberEnabled {
		t.Error("ContextNumberEnabled default = true, want false")
	}
}

func TestLoadConfig_ContextNumberOn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_CONTEXT_NUMBER_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !cfg.ContextNumberEnabled {
		t.Error("=true should enable")
	}
}

func TestLoadConfig_ContextNumberExplicitFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_CONTEXT_NUMBER_ENABLED=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.ContextNumberEnabled {
		t.Error("=false should stay disabled")
	}
}

func TestLoadConfig_RateBottomBarDefaultsFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.RateBottomBarEnabled {
		t.Error("RateBottomBarEnabled default = true, want false")
	}
}

func TestLoadConfig_RateBottomBarOn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_RATE_BOTTOM_BAR=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !cfg.RateBottomBarEnabled {
		t.Error("=true should enable")
	}
}

func TestLoadConfig_RateBottomBarExplicitFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_RATE_BOTTOM_BAR=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.RateBottomBarEnabled {
		t.Error("=false should stay disabled")
	}
}

func TestLoadConfig_RateResetDefaultsFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.RateResetEnabled {
		t.Error("RateResetEnabled default = true, want false")
	}
}

func TestLoadConfig_RateResetOn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_RATE_RESET=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !cfg.RateResetEnabled {
		t.Error("=true should enable")
	}
}

func TestLoadConfig_SourceCardDefaultsTrue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !cfg.SourceCardEnabled {
		t.Error("SourceCardEnabled default = false, want true")
	}
}

func TestLoadConfig_SourceCardDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_SOURCE_CARD=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.SourceCardEnabled {
		t.Error("EMBER_SOURCE_CARD=false should disable SourceCardEnabled")
	}
}

func TestLoadConfig_SessionBarDefaultsTrue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte("EMBER_SOURCE=mbp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !cfg.SessionBarEnabled {
		t.Error("SessionBarEnabled default = false, want true")
	}
}

func TestLoadConfig_SessionBarDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"),
		[]byte("EMBER_SOURCE=mbp\nEMBER_SESSION_BAR=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.SessionBarEnabled {
		t.Error("EMBER_SESSION_BAR=false should disable SessionBarEnabled")
	}
}
