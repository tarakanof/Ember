package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarakanof/ember/internal/producer"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("EMBER_TOKEN", "")
	envDir := filepath.Join(dir, ".config", "ember")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "producer.env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadConfigDefaults(t *testing.T) {
	home := writeEnv(t, "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://ember.lan:3627\nEMBER_TOKEN=tok\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "mbp" || cfg.ServerURL != "http://ember.lan:3627" || cfg.Token != "tok" {
		t.Errorf("identity = %+v", cfg)
	}
	if cfg.PollIntervalMs != 2000 || cfg.ActivityWindowSeconds != 300 {
		t.Errorf("intervals = %d/%d", cfg.PollIntervalMs, cfg.ActivityWindowSeconds)
	}
	if cfg.T3Home != filepath.Join(home, ".t3") {
		t.Errorf("T3Home = %q", cfg.T3Home)
	}
	if cfg.StateDir != filepath.Join(home, ".local", "state", "ember", "sessions") {
		t.Errorf("StateDir = %q", cfg.StateDir)
	}
	if !cfg.ActivityTrailEnabled || !cfg.SourceCardEnabled || !cfg.SessionBarEnabled {
		t.Errorf("default-on toggles off: %+v", cfg)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	writeEnv(t, "EMBER_SOURCE=mbp\nEMBER_T3_HOME=/tmp/t3home\nEMBER_T3_POLL_INTERVAL_MS=10\nEMBER_T3_ACTIVITY_WINDOW_SECONDS=30\nEMBER_ACTIVITY_TRAIL_ENABLED=off\nEMBER_SOURCE_CARD=false\nEMBER_SESSION_BAR=0\nEMBER_SOURCE_COLOR=#aa66ff\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.T3Home != "/tmp/t3home" || cfg.ActivityWindowSeconds != 30 || cfg.SourceColor != "#aa66ff" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.PollIntervalMs != minPollIntervalMs {
		t.Errorf("PollIntervalMs = %d, want clamped to %d", cfg.PollIntervalMs, minPollIntervalMs)
	}
	if cfg.ActivityTrailEnabled || cfg.SourceCardEnabled || cfg.SessionBarEnabled {
		t.Errorf("toggles not turned off: %+v", cfg)
	}
}

func TestLoadConfigTokenFromEnvironment(t *testing.T) {
	writeEnv(t, "EMBER_SOURCE=mbp\n")
	t.Setenv("EMBER_TOKEN", "from-env")
	cfg, _ := loadConfig()
	if cfg.Token != "from-env" {
		t.Errorf("Token = %q", cfg.Token)
	}
}

func TestConfigLogValueRedactsToken(t *testing.T) {
	cfg := Config{Common: producer.Common{Token: "super-secret"}}
	if s := cfg.LogValue().String(); s == "" || strings.Contains(s, "super-secret") {
		t.Fatalf("LogValue leaks token: %s", s)
	}
}

func TestLoadConfigSourceDefaultsFromHostOnPlaceholder(t *testing.T) {
	defer producer.SetHostNameForTest("Dmitrys-MacBook-Pro")()
	writeEnv(t, "EMBER_SOURCE=set-me-to-this-laptop-id\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if want := "mbp"; cfg.Source != want {
		t.Errorf("Source = %q, want %q", cfg.Source, want)
	}
	writeEnv(t, "EMBER_SOURCE=\n")
	if cfg, _ = loadConfig(); cfg.Source != "mbp" {
		t.Errorf("empty Source = %q, want mbp", cfg.Source)
	}
	writeEnv(t, "EMBER_SOURCE=m5\n")
	if cfg, _ = loadConfig(); cfg.Source != "m5" {
		t.Errorf("Source = %q, want m5", cfg.Source)
	}
}

func TestLoadConfigExpandsTildeInT3Home(t *testing.T) {
	home := writeEnv(t, "EMBER_T3_HOME=~/Work/t3\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Work", "t3"); cfg.T3Home != want {
		t.Errorf("T3Home = %q, want %q", cfg.T3Home, want)
	}
}
