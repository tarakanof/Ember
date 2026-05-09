package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigPath_Flag(t *testing.T) {
	got, src := resolveConfigPath("/tmp/x.json")
	if got != "/tmp/x.json" || src != "flag" {
		t.Errorf("resolveConfigPath(/tmp/x.json) = (%q, %q), want (/tmp/x.json, flag)", got, src)
	}
}

func TestResolveConfigPath_Env(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/tmp/y.json")
	got, src := resolveConfigPath("")
	if got != "/tmp/y.json" || src != "env" {
		t.Errorf("got (%q, %q), want (/tmp/y.json, env)", got, src)
	}
}

func TestResolveConfigPath_Cwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CONFIG_PATH", "")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, src := resolveConfigPath("")
	if got != "config.json" || src != "cwd" {
		t.Errorf("got (%q, %q), want (config.json, cwd)", got, src)
	}
}

func TestResolveConfigPath_Defaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CONFIG_PATH", "")
	got, src := resolveConfigPath("")
	if got != "" || src != "defaults" {
		t.Errorf("got (%q, %q), want (\"\", defaults)", got, src)
	}
}

func TestParseConfigFile_OK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfigFile(path)
	if err != nil {
		t.Fatalf("parseConfigFile: unexpected error: %v", err)
	}
	if cfg.AWTRIX.HTTPBaseURL != "http://x" {
		t.Errorf("AWTRIX.HTTPBaseURL = %q", cfg.AWTRIX.HTTPBaseURL)
	}
}

func TestParseConfigFile_MissingFile(t *testing.T) {
	_, err := parseConfigFile("/nonexistent/awtrix.json")
	if !errors.Is(err, ErrConfigRead) {
		t.Errorf("err = %v, want ErrConfigRead wrapped", err)
	}
}

func TestParseConfigFile_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseConfigFile(path)
	if !errors.Is(err, ErrConfigParse) {
		t.Errorf("err = %v, want ErrConfigParse wrapped", err)
	}
}

func TestValidateConfig_OK(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://192.168.0.14"
	if err := validateConfig(cfg); err != nil {
		t.Errorf("validateConfig: %v", err)
	}
}

func TestValidateConfig_MissingAWTRIXBase(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = ""
	err := validateConfig(cfg)
	if !errors.Is(err, ErrConfigValidate) {
		t.Errorf("err = %v, want ErrConfigValidate wrapped", err)
	}
	if !strings.Contains(err.Error(), "awtrix.http_base_url") {
		t.Errorf("err detail %q should mention awtrix.http_base_url", err)
	}
}

func TestValidateConfig_BadAWTRIXURL(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "://broken"
	if err := validateConfig(cfg); !errors.Is(err, ErrConfigValidate) {
		t.Errorf("err = %v, want ErrConfigValidate wrapped", err)
	}
}

func TestValidateConfig_RateLimitNegativeBurstFails(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.Burst = -1
	if err := validateConfig(cfg); !errors.Is(err, ErrConfigValidate) {
		t.Errorf("err = %v, want ErrConfigValidate wrapped", err)
	}
}

func TestValidateConfig_RateLimitNegativeRefillFails(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.RefillPerSec = -0.5
	if err := validateConfig(cfg); !errors.Is(err, ErrConfigValidate) {
		t.Errorf("err = %v, want ErrConfigValidate wrapped", err)
	}
}

func TestValidateConfig_RateLimitNegativeIdleEvictFails(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.IdleEvictSeconds = -10
	if err := validateConfig(cfg); !errors.Is(err, ErrConfigValidate) {
		t.Errorf("err = %v, want ErrConfigValidate wrapped", err)
	}
}

func TestValidateConfig_RateLimitDefaultsValid(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	if err := validateConfig(cfg); err != nil {
		t.Errorf("default rate-limit config should validate cleanly: %v", err)
	}
}

func TestApplyDefaults_RateLimitFillsZero(t *testing.T) {
	cfg := Config{}
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	if cfg.RateLimit.Burst != 10 {
		t.Errorf("RateLimit.Burst = %d, want 10", cfg.RateLimit.Burst)
	}
	if cfg.RateLimit.RefillPerSec != 2.0 {
		t.Errorf("RateLimit.RefillPerSec = %v, want 2.0", cfg.RateLimit.RefillPerSec)
	}
	if cfg.RateLimit.IdleEvictSeconds != 300 {
		t.Errorf("RateLimit.IdleEvictSeconds = %d, want 300", cfg.RateLimit.IdleEvictSeconds)
	}
}

func TestApplyDefaults_RateLimitPreservesDisabled(t *testing.T) {
	cfg := Config{}
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.RateLimit.Disabled = true
	cfg.applyDefaults()
	if !cfg.RateLimit.Disabled {
		t.Error("Disabled flag was overwritten by applyDefaults")
	}
}
