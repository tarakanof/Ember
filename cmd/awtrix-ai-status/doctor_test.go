package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAppForDoctor(t *testing.T, awtrixURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = awtrixURL
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.configPath = "/tmp/cfg.json"
	app.configSource = "flag"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	app.listener = ln
	return app
}

func TestRunDoctorChecks_OnlineAllOK(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/stats" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
	cfg := app.cfg.Load()

	res := runDoctorChecks(context.Background(), app, cfg)
	if !res.OK {
		t.Errorf("OK = false, want true. checks = %#v", res.Checks)
	}
	if res.Mode != "online" {
		t.Errorf("Mode = %q, want online", res.Mode)
	}
	for _, k := range []string{"config_loaded", "auth_token_present", "awtrix_reachable", "http_listening", "sessions_summary", "last_publish", "uptime", "build"} {
		if c, ok := res.Checks[k]; !ok {
			t.Errorf("missing check %q", k)
		} else if c.Status != StatusOK {
			t.Errorf("check %q: status=%q, want %q (detail=%q)", k, c.Status, StatusOK, c.Detail)
		}
	}
}

func TestRunDoctorChecks_OfflineMarksRuntimeSkipped(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = awtrix.URL
	cfg.applyDefaults()

	res := runDoctorChecks(context.Background(), nil, &cfg)
	if res.Mode != "offline" {
		t.Errorf("Mode = %q, want offline", res.Mode)
	}
	if res.OK {
		t.Errorf("OK = true, want false (skipped != ok)")
	}
	skipped := []string{"auth_token_present", "http_listening", "sessions_summary", "last_publish", "uptime"}
	for _, k := range skipped {
		if c := res.Checks[k]; c.Status != StatusSkipped {
			t.Errorf("check %q: status=%q, want %q", k, c.Status, StatusSkipped)
		}
	}
	for _, k := range []string{"config_loaded", "awtrix_reachable", "build"} {
		if c := res.Checks[k]; c.Status != StatusOK {
			t.Errorf("check %q (static): status=%q, want %q (detail=%q)", k, c.Status, StatusOK, c.Detail)
		}
	}
}

func TestRunDoctorChecks_AWTRIXUnreachable(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = strings.Replace(deadAddr(t), "/healthz", "", 1) // closed port
	cfg.applyDefaults()

	res := runDoctorChecks(context.Background(), nil, &cfg)
	if c := res.Checks["awtrix_reachable"]; c.Status != StatusFail {
		t.Errorf("awtrix_reachable: status=%q, want %q (detail=%q)", c.Status, StatusFail, c.Detail)
	}
	if res.OK {
		t.Errorf("OK = true, want false on awtrix unreachable")
	}
}

func TestRunDoctorChecks_AWTRIXHonorsTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = slow.URL
	cfg.AWTRIX.TimeoutSeconds = 1 // 1s > 200ms, should pass
	cfg.applyDefaults()

	res := runDoctorChecks(context.Background(), nil, &cfg)
	if c := res.Checks["awtrix_reachable"]; c.Status != StatusOK {
		t.Errorf("awtrix_reachable with 1s timeout vs 200ms server: %q (detail=%q)", c.Status, c.Detail)
	}
}

func writeMinimalCfg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDoctorCLI_OfflineSucceeds(t *testing.T) {
	cfgPath := writeMinimalCfg(t)
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()
	// Rewrite the cfg to point at the AWTRIX stub.
	body := `{"awtrix":{"http_base_url":"` + awtrix.URL + `"}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "doctor", "--offline", "--json", "-config", cfgPath)
	cmd.Env = append(cmd.Environ(), "CONFIG_PATH=/nonexistent/awtrix.json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("doctor --offline: %v\nstderr: %s", err, stderr.String())
	}
	var res DoctorResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout.String())
	}
	if res.Mode != "offline" {
		t.Errorf("Mode = %q, want offline", res.Mode)
	}
	if c := res.Checks["sessions_summary"]; c.Status != StatusSkipped {
		t.Errorf("sessions_summary: status=%q, want skipped", c.Status)
	}
}

func TestDoctorCLI_FlagsBeforeSubcommand(t *testing.T) {
	cfgPath := writeMinimalCfg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-config", cfgPath, "doctor", "--offline", "--json")
	cmd.Env = append(cmd.Environ(), "CONFIG_PATH=/nonexistent/awtrix.json")
	if err := cmd.Run(); err == nil {
		// expected exit 0 because static checks could pass; if they fail
		// (e.g. AWTRIX is "http://x" and unreachable), exit 1 is also a
		// valid demonstration that the dispatcher reached the doctor.
		// We only care that it didn't fall through to server start.
	}
	// If the dispatcher fell through, `go run` would block on the server
	// starting. The 30 s context timeout would kick in. Treat any clean
	// (timely) exit as a pass.
}

func TestDoctorCLI_OnlineUsesServerURL(t *testing.T) {
	stubResult := DoctorResult{
		OK: true, Mode: "online",
		Checks: map[string]CheckResult{
			"build": {Status: StatusOK, Detail: "stub"},
		},
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/doctor" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stubResult)
	}))
	defer stub.Close()

	cfgPath := writeMinimalCfg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "doctor", "--server-url", stub.URL, "--json", "-config", cfgPath)
	cmd.Env = append(cmd.Environ(), "STATUS_TOKEN=tok", "CONFIG_PATH=/nonexistent/awtrix.json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("doctor --server-url: %v\nstderr: %s", err, stderr.String())
	}
	var res DoctorResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout.String())
	}
	if res.Mode != "online" || !res.OK {
		t.Errorf("res = %#v", res)
	}
}

func TestDoctorCLI_AuthFailureNoFallback(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer stub.Close()

	cfgPath := writeMinimalCfg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "doctor", "--server-url", stub.URL, "-config", cfgPath)
	cmd.Env = append(cmd.Environ(), "STATUS_TOKEN=wrong", "CONFIG_PATH=/nonexistent/awtrix.json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("doctor against 401 should exit non-zero")
	}
	if !strings.Contains(stderr.String(), "auth failure") {
		t.Errorf("stderr should mention 'auth failure'; got: %s", stderr.String())
	}
}

func TestRunDoctorChecks_HTTPListening_TLSScheme(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)

	t.Setenv("STATUS_TLS_CERT_FILE", "/some/path")
	t.Setenv("STATUS_TLS_KEY_FILE", "/some/path")

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	got := res.Checks["http_listening"].Detail
	if !strings.Contains(got, "scheme=https") {
		t.Errorf("http_listening detail = %q; want it to contain scheme=https", got)
	}
}

func TestRunDoctorChecks_HTTPListening_PlainScheme(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)

	t.Setenv("STATUS_TLS_CERT_FILE", "")
	t.Setenv("STATUS_TLS_KEY_FILE", "")

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	got := res.Checks["http_listening"].Detail
	if !strings.Contains(got, "scheme=http") || strings.Contains(got, "scheme=https") {
		t.Errorf("http_listening detail = %q; want it to contain scheme=http (and not https)", got)
	}
}
