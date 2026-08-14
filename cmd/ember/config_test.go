package main

import (
	"bytes"
	"errors"
	"log/slog"
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

// TestParseConfigFile_LegacyPulseStyleStillParses asserts that configs
// carrying the now-removed "pulse_style" field continue to load. Without
// the deprecated struct shim, DisallowUnknownFields would break every
// existing deployment that started from the G.1b config.example.json.
func TestParseConfigFile_LegacyPulseStyleStillParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	body := `{"awtrix":{"http_base_url":"http://x"},"display":{"pulse_style":"breathe"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseConfigFile(path); err != nil {
		t.Fatalf("parseConfigFile rejected legacy pulse_style: %v", err)
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
	if cfg.RateLimit.Burst != 60 {
		t.Errorf("RateLimit.Burst = %d, want 60", cfg.RateLimit.Burst)
	}
	if cfg.RateLimit.RefillPerSec != 5.0 {
		t.Errorf("RateLimit.RefillPerSec = %v, want 5.0", cfg.RateLimit.RefillPerSec)
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

// TestLoadConfig_InvalidDeviceURLSchemeFallsBackToDefault covers the SSRF
// guard on the config.json baseline: a hand-edited file:/gopher:/bare-path
// base_url must not crash the server at startup — it's logged and replaced
// with the safe default so the rest of the config still loads.
func TestLoadConfig_InvalidDeviceURLSchemeFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	body := `{"awtrix":{"http_base_url":"file:///etc/passwd"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg, err := loadConfig(path, logger)
	if err != nil {
		t.Fatalf("loadConfig should not fail on an invalid baseline scheme, got: %v", err)
	}
	if cfg.AWTRIX.HTTPBaseURL != defaultDeviceBaseURL {
		t.Errorf("HTTPBaseURL = %q, want fallback to default %q", cfg.AWTRIX.HTTPBaseURL, defaultDeviceBaseURL)
	}
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "awtrix.http_base_url invalid") {
		t.Errorf("expected a WARN log for the invalid baseline URL through the supplied logger, got: %q", got)
	}
}

// TestLoadConfig_InvalidIconIDsDroppedWithWarn covers the icon-id SSRF-ish
// guard (path traversal into /ICONS) at the config.json baseline: invalid
// entries are dropped (logged), valid ones kept, load still succeeds.
func TestLoadConfig_InvalidIconIDsDroppedWithWarn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	body := `{"awtrix":{"http_base_url":"http://x"},"weather":{"icon_ids":{"clear":"123","clouds":"../dev"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg, err := loadConfig(path, logger)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Weather.IconIDs["clear"] != "123" {
		t.Errorf("valid icon id was dropped: %+v", cfg.Weather.IconIDs)
	}
	if _, ok := cfg.Weather.IconIDs["clouds"]; ok {
		t.Errorf("invalid icon id was not dropped: %+v", cfg.Weather.IconIDs)
	}
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "weather.icon_ids entry invalid") {
		t.Errorf("expected a WARN log for the dropped icon id through the supplied logger, got: %q", got)
	}
}

// TestSanitizeConfigBaseline_LogsThroughSuppliedLogger is a narrower unit
// test on sanitizeConfigBaseline itself (independent of file loading),
// proving both drop paths write to the logger passed in, not slog.Default().
func TestSanitizeConfigBaseline_LogsThroughSuppliedLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "file:///etc/passwd"
	cfg.Weather.IconIDs = map[string]string{"clouds": "../dev"}

	sanitizeConfigBaseline(&cfg, logger)

	got := logs.String()
	if !strings.Contains(got, "awtrix.http_base_url invalid") {
		t.Errorf("missing device-URL warning in captured logs: %q", got)
	}
	if !strings.Contains(got, "weather.icon_ids entry invalid") {
		t.Errorf("missing icon-id warning in captured logs: %q", got)
	}
}

func TestPomodoroDefaultsCapAndFocusMax(t *testing.T) {
	c := defaultConfig()
	if c.Pomodoro.MaxSessionMinutes != 480 {
		t.Fatalf("default max_session_minutes = %d, want 480", c.Pomodoro.MaxSessionMinutes)
	}
}

func TestValidatePomodoroFocusMaxAndCapRanges(t *testing.T) {
	base := defaultConfig().Pomodoro
	base.Enabled = true

	ok := base
	ok.FocusMinutes = 480
	ok.MaxSessionMinutes = 0 // 0 = off is valid
	if err := validatePomodoro(ok); err != nil {
		t.Fatalf("focus=480 cap=0 should be valid, got %v", err)
	}

	bad := base
	bad.FocusMinutes = 481
	if err := validatePomodoro(bad); err == nil {
		t.Fatal("focus=481 should be rejected")
	}

	badCap := base
	badCap.MaxSessionMinutes = 1441
	if err := validatePomodoro(badCap); err == nil {
		t.Fatal("max_session_minutes=1441 should be rejected")
	}
}
