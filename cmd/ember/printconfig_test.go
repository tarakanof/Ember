package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactConfig_TokenSet(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = "secret-xyz"
	cfg.AWTRIX.HTTPBaseURL = "http://192.168.0.14"

	out := redactConfig(cfg)
	if out.Auth.StatusToken != "<redacted>" {
		t.Errorf("Auth.StatusToken = %q, want \"<redacted>\"", out.Auth.StatusToken)
	}
	if out.AWTRIX.HTTPBaseURL != "http://192.168.0.14" {
		t.Errorf("AWTRIX.HTTPBaseURL changed unexpectedly: %q", out.AWTRIX.HTTPBaseURL)
	}
}

func TestRedactConfig_TokenUnset(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = ""

	out := redactConfig(cfg)
	if out.Auth.StatusToken != "" {
		t.Errorf("Auth.StatusToken = %q, want \"\"", out.Auth.StatusToken)
	}
}

func TestRedactConfig_StripsURLUserinfo(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.AWTRIX.HTTPBaseURL = "http://user:pass@192.168.0.14:7000/path"

	out := redactConfig(cfg)
	if strings.Contains(out.AWTRIX.HTTPBaseURL, "user") || strings.Contains(out.AWTRIX.HTTPBaseURL, "pass") {
		t.Errorf("userinfo not stripped: %q", out.AWTRIX.HTTPBaseURL)
	}
	if out.AWTRIX.HTTPBaseURL != "http://192.168.0.14:7000/path" {
		t.Errorf("redacted URL = %q, want \"http://192.168.0.14:7000/path\"", out.AWTRIX.HTTPBaseURL)
	}
}

func TestPrintConfig_Subprocess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	body := `{"awtrix":{"http_base_url":"http://1.2.3.4"},"auth":{"status_token_env":"X_TOK"}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "--print-config", "-config", cfgPath)
	cmd.Env = append(cmd.Environ(), "X_TOK=tokval")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("--print-config: %v\nstderr: %s", err, stderr.String())
	}

	var got Config
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal stdout: %v\nstdout: %s", err, stdout.String())
	}
	if got.Auth.StatusToken != "<redacted>" {
		t.Errorf("Auth.StatusToken = %q, want \"<redacted>\" (env was set)", got.Auth.StatusToken)
	}
	if got.AWTRIX.HTTPBaseURL != "http://1.2.3.4" {
		t.Errorf("AWTRIX.HTTPBaseURL = %q", got.AWTRIX.HTTPBaseURL)
	}
}
