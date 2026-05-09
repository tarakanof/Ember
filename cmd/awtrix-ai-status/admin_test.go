package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func writeCfg(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newAppForReload(t *testing.T, cfgBody string) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	path := writeCfg(t, dir, cfgBody)

	cfg, err := parseConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if err := validateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Auth.StatusToken = "tok"
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.configPath = path
	app.configSource = "flag"
	return app, path
}

func TestAdminReload_HappyPath(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"old"}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"new"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
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
	var body200 struct {
		Reloaded      bool     `json:"reloaded"`
		ChangedFields []string `json:"changed_fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body200); err != nil {
		t.Fatal(err)
	}
	if !body200.Reloaded || len(body200.ChangedFields) != 1 || body200.ChangedFields[0] != "display.idle_text" {
		t.Errorf("body = %#v, want reloaded=true changed=[display.idle_text]", body200)
	}
	if app.cfg.Load().Display.IdleText != "new" {
		t.Errorf("cfg.Load().Display.IdleText = %q, want \"new\"", app.cfg.Load().Display.IdleText)
	}
}

func TestAdminReload_NonReloadable409(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"http://x"},"display":{"refresh_seconds":999}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "display.refresh_seconds=") {
		t.Errorf("body should mention old→new values: %s", respBody)
	}
	if app.cfg.Load().Display.RefreshSeconds == 999 {
		t.Errorf("cfg unchanged check failed; got %d", app.cfg.Load().Display.RefreshSeconds)
	}
}

func TestAdminReload_ParseError400(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminReload_ValidationError422(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":""}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestAdminReload_FromDefaults412(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.configPath = ""
	app.configSource = "defaults"

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", resp.StatusCode)
	}
}

func TestHandleAdminReload_413OnOversizeBody(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	app, _ := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	payload := bytes.Repeat([]byte("x"), 2048)
	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized /admin/reload body: code = %d, want 413 or 400", resp.StatusCode)
	}
}
