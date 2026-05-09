package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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
