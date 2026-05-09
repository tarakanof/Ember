package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionHandler_PublicAndJSON(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"binary", "revision", "dirty", "go_version"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing field %q (got %#v)", k, body)
		}
	}
	if body["binary"] != "awtrix-ai-status" {
		t.Errorf("binary = %v, want awtrix-ai-status", body["binary"])
	}
}

func TestAdminDoctor_NoTokenFailsClosed(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = "" // unset
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/doctor")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminDoctor_WrongTokenIs401(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = "right"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/admin/doctor", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminDoctor_OKReturnsResult(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = awtrix.URL
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.configPath = "/tmp/x.json"
	app.configSource = "flag"
	// Wire a listener so http_listening reports OK rather than Skipped.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	app.listener = ln

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/admin/doctor", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body DoctorResult
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "online" {
		t.Errorf("Mode = %q", body.Mode)
	}
	// app.listener is wired above, so http_listening reports OK. With a happy
	// AWTRIX upstream and a writable config path, all eight checks should pass
	// and the overall result is OK with status 200.
	if !body.OK {
		t.Errorf("OK = false, want true; checks = %#v", body.Checks)
	}
	if _, ok := body.Checks["awtrix_reachable"]; !ok {
		t.Errorf("checks missing awtrix_reachable: %#v", body.Checks)
	}
}

func TestAdminDoctor_FailReturns503(t *testing.T) {
	cfg := defaultConfig()
	dead := deadAddr(t)
	// Trim trailing /healthz from deadAddr; AWTRIX probe appends /api/stats itself.
	cfg.AWTRIX.HTTPBaseURL = strings.TrimSuffix(dead, "/healthz")
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/admin/doctor", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (any check failed)", resp.StatusCode)
	}
}
