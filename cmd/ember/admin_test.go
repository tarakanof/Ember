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

// TestDiffConfig_ReportsChangeInEveryTopLevelSection guards against the
// hand-rolled leaf list going stale: diffConfig must surface a changed path
// for every top-level Config section, including ones with no dedicated leaf
// entries today (Weather, Meetings, QuietHours, Pomodoro, usage toggles).
func TestDiffConfig_ReportsChangeInEveryTopLevelSection(t *testing.T) {
	base := defaultConfig()
	base.applyDefaults()

	cases := []struct {
		name       string
		mutate     func(*Config)
		wantPrefix string
	}{
		{"http", func(c *Config) { c.HTTP.Addr = c.HTTP.Addr + "x" }, "http."},
		{"awtrix", func(c *Config) { c.AWTRIX.AppName = c.AWTRIX.AppName + "x" }, "awtrix."},
		{"auth", func(c *Config) { c.Auth.StatusToken = c.Auth.StatusToken + "x" }, "auth."},
		{"display", func(c *Config) { c.Display.IdleText = c.Display.IdleText + "x" }, "display."},
		{"rate_limit", func(c *Config) { c.RateLimit.Burst++ }, "rate_limit."},
		{"pomodoro", func(c *Config) { c.Pomodoro.Enabled = !c.Pomodoro.Enabled }, "pomodoro."},
		{"weather", func(c *Config) { c.Weather.Enabled = !c.Weather.Enabled }, "weather."},
		{"meetings", func(c *Config) { c.Meetings.TileLeadMinutes++ }, "meetings."},
		{"quiet_hours", func(c *Config) { c.QuietHours.Enabled = !c.QuietHours.Enabled }, "quiet_hours."},
		{"usage_widget", func(c *Config) { b := true; c.UsageWidget = &b }, "usage_widget"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newCfg := base
			tc.mutate(&newCfg)
			changed := diffConfig(base, newCfg)
			for _, f := range changed {
				if strings.HasPrefix(f, tc.wantPrefix) {
					return
				}
			}
			t.Errorf("diffConfig missed section %q: changed=%v", tc.name, changed)
		})
	}
}

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
	for _, k := range []string{"binary", "version", "revision", "dirty", "go_version"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing field %q (got %#v)", k, body)
		}
	}
	if body["binary"] != "ember" {
		t.Errorf("binary = %v, want ember", body["binary"])
	}
}

func TestVersionHandler_ReportsInjectedRelease(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Stands in for a release build's -ldflags "-X main.version=v0.22.0".
	app.versionInfo.Version = "v0.22.0"

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if got := fetchVersion(t, srv).Version; got != "v0.22.0" {
		t.Errorf("version = %q, want v0.22.0", got)
	}
}

func TestVersionHandler_ReportsDevWithoutInjectedRelease(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// The test binary carries no -X main.version, like a local source build.
	if got := fetchVersion(t, srv).Version; got != "dev" {
		t.Errorf("version = %q, want dev", got)
	}
}

func fetchVersion(t *testing.T, srv *httptest.Server) versionInfo {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var v versionInfo
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
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

// TestAdminReload_InvalidIconIDsDroppedWithWarn mirrors
// TestLoadConfig_InvalidIconIDsDroppedWithWarn (config_test.go) but exercises
// the /admin/reload path: it must run the same sanitizeConfigBaseline repair
// as startup, so a hand-edited config.json containing a path-traversal
// weather.icon_ids value is dropped rather than loaded live.
func TestAdminReload_InvalidIconIDsDroppedWithWarn(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"},"weather":{"icon_ids":{"clear":"123"}}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	newBody := `{"awtrix":{"http_base_url":"http://x"},"weather":{"icon_ids":{"clear":"123","clouds":"../dev"}}}`
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
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
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, respBody)
	}
	got := app.cfg.Load().Weather.IconIDs
	if got["clear"] != "123" {
		t.Errorf("valid icon id was dropped: %+v", got)
	}
	if _, ok := got["clouds"]; ok {
		t.Errorf("path-traversal icon id survived reload: %+v", got)
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

func TestAdminRequireAuth_LogsInfoOnEmptyToken(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.Auth.StatusToken = ""
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, captureLogger(&buf))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/doctor")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !strings.Contains(buf.String(), `"msg":"admin disabled"`) {
		t.Errorf("expected 'admin disabled' Info log, got: %s", buf.String())
	}
}

func TestAdminReload_LogsOutcome(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://1.2.3.4"}}`
	app, path := newAppForReload(t, body)
	var buf bytes.Buffer
	app.logger = captureLogger(&buf)

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"msg":"admin reload"`) ||
		!strings.Contains(logs, `"status":200`) {
		t.Errorf("expected 'admin reload' status=200 log, got: %s", logs)
	}
}

func TestAdminReload_RateLimitRefillReloaded(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"},"rate_limit":{"refill_per_sec":2}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	newBody := `{"awtrix":{"http_base_url":"http://x"},"rate_limit":{"refill_per_sec":5}}`
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
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
	var out struct {
		ChangedFields []string `json:"changed_fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range out.ChangedFields {
		if f == "rate_limit.refill_per_sec" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("changed_fields = %v, want to include rate_limit.refill_per_sec", out.ChangedFields)
	}
	if app.cfg.Load().RateLimit.RefillPerSec != 5 {
		t.Errorf("RefillPerSec = %v, want 5", app.cfg.Load().RateLimit.RefillPerSec)
	}
}

func TestAdminReload_RateLimitDisabledFlipped(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	newBody := `{"awtrix":{"http_base_url":"http://x"},"rate_limit":{"disabled":true}}`
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
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
	if !app.cfg.Load().RateLimit.Disabled {
		t.Error("Disabled = false, want true after reload")
	}
}

// TestAdminReload_G2FrameLifetimeReloaded verifies that
// display.frame_lifetime_seconds appears in changed_fields after a reload
// that mutates it, and that the new value is applied.
func TestAdminReload_G2FrameLifetimeReloaded(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"},"display":{"frame_lifetime_seconds":30}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	newBody := `{"awtrix":{"http_base_url":"http://x"},"display":{"frame_lifetime_seconds":60}}`
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
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
	var out struct {
		ChangedFields []string `json:"changed_fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range out.ChangedFields {
		if f == "display.frame_lifetime_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("changed_fields = %v, want to include display.frame_lifetime_seconds", out.ChangedFields)
	}
	if app.cfg.Load().Display.FrameLifetimeSeconds != 60 {
		t.Errorf("FrameLifetimeSeconds = %d, want 60", app.cfg.Load().Display.FrameLifetimeSeconds)
	}
}

// TestAdminReload_G2IdleRestoreReloaded verifies that
// display.idle_restore_seconds appears in changed_fields after a reload
// that mutates it, and that the new value is applied.
func TestAdminReload_G2IdleRestoreReloaded(t *testing.T) {
	body := `{"awtrix":{"http_base_url":"http://x"},"display":{"idle_restore_seconds":1200}}`
	app, path := newAppForReload(t, body)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	newBody := `{"awtrix":{"http_base_url":"http://x"},"display":{"idle_restore_seconds":600}}`
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
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
	var out struct {
		ChangedFields []string `json:"changed_fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range out.ChangedFields {
		if f == "display.idle_restore_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("changed_fields = %v, want to include display.idle_restore_seconds", out.ChangedFields)
	}
	if app.cfg.Load().Display.IdleRestoreSeconds != 600 {
		t.Errorf("IdleRestoreSeconds = %d, want 600", app.cfg.Load().Display.IdleRestoreSeconds)
	}
}
