package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvFile_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.env")
	got, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("missing file should not be an error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty map, got %v", got)
	}
}

func TestReadEnvFile_StrictPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "producer.env")
	if err := os.WriteFile(path, []byte("STATUS_SOURCE=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readEnvFile(path)
	if err == nil {
		t.Errorf("0644 file should error; got %v", got)
	}
}

func TestReadEnvFile_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.env")
	if err := os.WriteFile(target, []byte("STATUS_SOURCE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "producer.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := readEnvFile(link)
	if err == nil {
		t.Errorf("symlink should error")
	}
}

func TestReadEnvFile_ValidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "producer.env")
	content := `# comment
STATUS_SOURCE=dt-mbp
STATUS_SERVER_URL="http://192.168.0.36:8080"
STATUS_TOKEN=abc123

# another comment
STATUS_HOOK_TIMEOUT_MS=250
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"STATUS_SOURCE":          "dt-mbp",
		"STATUS_SERVER_URL":      "http://192.168.0.36:8080",
		"STATUS_TOKEN":           "abc123",
		"STATUS_HOOK_TIMEOUT_MS": "250",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

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
