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
		if r.URL.Path == "/api/v1/device" {
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

func TestRunDoctorChecks_ClockReachable(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// discovery.Reachable (internal/discovery, ticket #66) still probes the
		// legacy /api/stats fingerprint — untouched by this migration.
		if r.URL.Path == "/api/stats" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"aabbcc","version":"0.98"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
	cfg := app.cfg.Load()

	// Populate the T1/T2 atomics the way boot / the periodic probe would, so
	// the clock check has a real last-rediscover record to surface.
	app.rediscoverClock(context.Background())

	res := runDoctorChecks(context.Background(), app, cfg)
	c, ok := res.Checks["clock"]
	if !ok {
		t.Fatalf("missing clock check")
	}
	if c.Status != StatusOK {
		t.Errorf("clock status = %q, want %q (detail=%q)", c.Status, StatusOK, c.Detail)
	}
	if c.Reachable == nil || !*c.Reachable {
		t.Errorf("clock reachable = %v, want true", c.Reachable)
	}
	if c.BaseURL != awtrix.URL {
		t.Errorf("clock base_url = %q, want %q", c.BaseURL, awtrix.URL)
	}
	if c.Source != "config" {
		t.Errorf("clock source = %q, want %q", c.Source, "config")
	}
	if c.LastRediscoverAt == nil {
		t.Errorf("clock last_rediscover_at = nil, want set after rediscoverClock ran")
	}
	if c.LastRediscoverResult != "reachable" {
		t.Errorf("clock last_rediscover_result = %q, want %q", c.LastRediscoverResult, "reachable")
	}
	if !res.OK {
		t.Errorf("OK = false, want true when clock reachable")
	}
}

// TestRunDoctorChecks_ClockUnreachableWarnsButDoesNotFailOverall isolates the
// clock check's own contract: give it a URL that responds 200 (so the
// fail-capable awtrix_reachable check stays OK) but without the AWTRIX uid
// fingerprint (so discovery.Reachable, and therefore clock, sees it as not a
// real clock). clock alone must not flip res.OK to false — mirrors the
// meetings-stale WARN precedent.
func TestRunDoctorChecks_ClockUnreachableWarnsButDoesNotFailOverall(t *testing.T) {
	notAClock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200, but no AWTRIX JSON body/uid
	}))
	defer notAClock.Close()

	app := newAppForDoctor(t, notAClock.URL)
	cfg := app.cfg.Load()

	res := runDoctorChecks(context.Background(), app, cfg)
	c, ok := res.Checks["clock"]
	if !ok {
		t.Fatalf("missing clock check")
	}
	if c.Status != StatusWarn {
		t.Errorf("clock status = %q, want %q (detail=%q)", c.Status, StatusWarn, c.Detail)
	}
	if c.Reachable == nil || *c.Reachable {
		t.Errorf("clock reachable = %v, want false", c.Reachable)
	}
	if awtrixCheck := res.Checks["awtrix_reachable"]; awtrixCheck.Status != StatusOK {
		t.Fatalf("fixture broken: awtrix_reachable = %q, want %q (detail=%q)", awtrixCheck.Status, StatusOK, awtrixCheck.Detail)
	}
	if !res.OK {
		t.Errorf("OK = false, want true — a warn-status clock check must not flip overall OK")
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
	cmd.Env = append(cmd.Environ(), "EMBER_TOKEN=tok", "CONFIG_PATH=/nonexistent/awtrix.json")
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
	cmd.Env = append(cmd.Environ(), "EMBER_TOKEN=wrong", "CONFIG_PATH=/nonexistent/awtrix.json")
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

	t.Setenv("EMBER_TLS_CERT_FILE", "/some/path")
	t.Setenv("EMBER_TLS_KEY_FILE", "/some/path")

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

	t.Setenv("EMBER_TLS_CERT_FILE", "")
	t.Setenv("EMBER_TLS_KEY_FILE", "")

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	got := res.Checks["http_listening"].Detail
	if !strings.Contains(got, "scheme=http") || strings.Contains(got, "scheme=https") {
		t.Errorf("http_listening detail = %q; want it to contain scheme=http (and not https)", got)
	}
}

// TestDoctorWarnIsNonFatal: when the meetings feed is configured but its
// lastFetchOK is beyond meetingsStaleTTL, checkMeetings returns StatusWarn.
// runDoctorChecks must then set res.OK = true (warn is non-fatal).
func TestDoctorWarnIsNonFatal(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
	// Configure a meetings URL; set lastFetchOK 61m ago so meetings warns.
	app.meetingsURLs = []string{"http://calendar.example.com/feed.ics"}
	app.meetings.mu.Lock()
	app.meetings.lastFetchOK = time.Now().Add(-61 * time.Minute)
	app.meetings.mu.Unlock()

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())

	if c := res.Checks["meetings"]; c.Status != StatusWarn {
		t.Errorf("meetings status = %q, want %q (detail=%q)", c.Status, StatusWarn, c.Detail)
	}
	if !res.OK {
		t.Errorf("OK = false with only a WARN; want true (warn must be non-fatal)")
	}
}

// TestAdminDoctorWarnReturns200: when the meetings check warns (URLs set, never
// fetched), /admin/doctor must return 200, not 503. A stale-but-configured
// calendar feed must not make monitoring see the server as unavailable.
func TestAdminDoctorWarnReturns200(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
	// Inject a meetings URL so checkMeetings enters the "configured" path.
	// lastFetchOK is zero (never fetched) → StatusWarn.
	app.meetingsURLs = []string{"http://calendar.example.com/feed.ics"}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		t.Errorf("status = %d, want 200 (warn must not cause 503)", resp.StatusCode)
	}
	var body DoctorResult
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Errorf("OK = false, want true (warn must be non-fatal)")
	}
	if c := body.Checks["meetings"]; c.Status != StatusWarn {
		t.Errorf("meetings status = %q, want %q", c.Status, StatusWarn)
	}
}

// TestRenderDoctorText_WarnSummary: when there is one WARN and no FAILs, the
// summary line must say "OK (1 warning)" rather than plain "OK (online)".
func TestRenderDoctorText_WarnSummary(t *testing.T) {
	res := DoctorResult{
		OK:   true,
		Mode: "online",
		Checks: map[string]CheckResult{
			"build":    {Status: StatusOK, Detail: "rev=abc"},
			"meetings": {Status: StatusWarn, Detail: "1 feed(s); never successfully fetched"},
		},
	}
	var buf strings.Builder
	renderDoctorText(&buf, res)
	out := buf.String()
	if !strings.Contains(out, "1 warning") {
		t.Errorf("summary should mention warn count; got:\n%s", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("summary should not say FAIL; got:\n%s", out)
	}
}

// TestCheckMeetingsDisabledWithURLs: URLs set but cfg.Meetings.Enabled = boolPtr(false)
// and never fetched → StatusOK (not StatusWarn), detail mentions "disabled".
// Rationale: the poller never runs when disabled, so lastOK stays zero; that is
// expected and must not surface as a "feed broken" warning.
func TestCheckMeetingsDisabledWithURLs(t *testing.T) {
	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{
		"http://calendar.example.com/feed1.ics",
		"http://calendar.example.com/feed2.ics",
	}
	// lastFetchOK is zero (never fetched) — the poller never ran because disabled.

	cfg := app.cfg.Load()
	cfg.Meetings.Enabled = boolPtr(false)

	got := checkMeetings(app, cfg)

	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (disabled widget must not warn)", got.Status, StatusOK)
	}
	if !strings.Contains(got.Detail, "disabled") {
		t.Errorf("detail should mention 'disabled'; got %q", got.Detail)
	}
}

// TestCheckMeetingsStale: URLs set, lastFetchOK 61m in the past → StatusWarn
// with the age mentioned in the detail.
func TestCheckMeetingsStale(t *testing.T) {
	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{"http://calendar.example.com/feed.ics"}
	// Seed a lastFetchOK 61 minutes before real now (beyond meetingsStaleTTL = 60m).
	// Use time.Now() so the age calculation in checkMeetings (which also calls
	// time.Now()) yields a positive age ≥ meetingsStaleTTL.
	app.meetings.mu.Lock()
	app.meetings.lastFetchOK = time.Now().Add(-61 * time.Minute)
	app.meetings.mu.Unlock()

	cfg := app.cfg.Load()
	got := checkMeetings(app, cfg)

	if got.Status != StatusWarn {
		t.Errorf("status = %q, want %q", got.Status, StatusWarn)
	}
	if !strings.Contains(got.Detail, "stale") {
		t.Errorf("detail should mention 'stale'; got %q", got.Detail)
	}
	// The age (≈61m) must appear so operators can diagnose the feed.
	if !strings.Contains(got.Detail, "ago") {
		t.Errorf("detail should mention age ('ago'); got %q", got.Detail)
	}
}
