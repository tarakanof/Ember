package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/tarakanof/ember/internal/awtrix"
)

func newAppForDoctor(t *testing.T, awtrixURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = awtrixURL
	cfg.Auth.StatusToken = "tok"
	cfg.applyDefaults()
	app := guardApp(t, NewApp(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil))))
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
		if r.URL.Path == "/api/v1/device" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"aabbcc","boardType":"awtrixng","version":"1.0.13"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
	cfg := app.cfg.Load()

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

func TestRunDoctorChecks_ClockUnreachableWarnsButDoesNotFailOverall(t *testing.T) {
	notAClock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
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
	cfg.AWTRIX.HTTPBaseURL = strings.Replace(deadAddr(t), "/healthz", "", 1)
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
	cfg.AWTRIX.TimeoutSeconds = 1
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := goRunCommand(ctx, "doctor", "--offline", "--json", "-config", cfgPath)
	cmd.Env = append(isolatedGoRunEnv(t), "CONFIG_PATH=/nonexistent/awtrix.json")
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
	if c := res.Checks["awtrix_reachable"]; c.Status != StatusOK || !strings.Contains(c.Detail, "disabled") {
		t.Errorf("awtrix_reachable = %+v, want ok/disabled under EMBER_CLOCK=off", c)
	}
}

func TestDoctorCLI_FlagsBeforeSubcommand(t *testing.T) {
	runDoctorWithLeadingConfig := func(t *testing.T, body string, leading func(path string) []string, extraEnv ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
		t.Helper()
		cfgPath := filepath.Join(t.TempDir(), "cfg.json")
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		args := leading(cfgPath)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		cmd := goRunCommand(ctx, append(args, "doctor", "--offline", "--json")...)
		cmd.Env = append(isolatedGoRunEnv(t), extraEnv...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			t.Fatalf("doctor timed out: %v\nstderr: %s", ctx.Err(), stderr.String())
		}
		return cmd, &stdout, &stderr, err
	}

	t.Run("valid config wins over a broken CONFIG_PATH", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "broken.json")
		if err := os.WriteFile(broken, []byte(`{"not_a_config_field":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, stdout, stderr, err := runDoctorWithLeadingConfig(t, `{"awtrix":{"http_base_url":"http://x"},"http":{"addr":"127.0.0.1:0"}}`, func(p string) []string { return []string{"-config", p} }, "CONFIG_PATH="+broken)
		if err != nil {
			t.Fatalf("-config before doctor: %v\nstderr: %s", err, stderr.String())
		}
		var res DoctorResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v\nstdout: %s", err, stdout.String())
		}
		if res.Mode != "offline" {
			t.Errorf("Mode = %q, want offline", res.Mode)
		}
	})

	for name, leading := range map[string]func(string) []string{
		"-config":  func(p string) []string { return []string{"-config", p} },
		"--config": func(p string) []string { return []string{"--config", p} },
		"-config=": func(p string) []string { return []string{"-config=" + p} },
	} {
		t.Run("given config is the one read/"+name, func(t *testing.T) {
			cmd, _, stderr, err := runDoctorWithLeadingConfig(t, `{"not_a_config_field":true}`, leading)
			if err == nil {
				t.Fatalf("doctor read the CONFIG_PATH fallback, not the leading %s file\nargs: %v", name, cmd.Args)
			}
			if !strings.Contains(stderr.String(), "doctor: config:") || !strings.Contains(stderr.String(), "not_a_config_field") {
				t.Fatalf("stderr = %q, want a parse error naming the given file's unknown field", stderr.String())
			}
		})
	}
}

func TestCLI_ArgsThatWouldBeDroppedAreRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"flag before subcommand": {[]string{"-print-config", "doctor"}, "flag -print-config before subcommand doctor"},
		"subcommand after --":    {[]string{"--", "doctor"}, `unexpected argument "doctor"`},
		"unknown subcommand":     {[]string{"doctr"}, `unexpected argument "doctr"`},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := goRunCommand(ctx, tc.args...)
			cmd.Env = isolatedGoRunEnv(t)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
				t.Fatalf("%q: err = %v (ctx %v), want a non-zero exit\nstdout: %s\nstderr: %s", tc.args, err, ctx.Err(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%q: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
			}
		})
	}
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
	cmd := goRunCommand(ctx, "doctor", "--server-url", stub.URL, "--json", "-config", cfgPath)
	cmd.Env = append(isolatedGoRunEnv(t), "EMBER_TOKEN=tok", "CONFIG_PATH=/nonexistent/awtrix.json")
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
	cmd := goRunCommand(ctx, "doctor", "--server-url", stub.URL, "-config", cfgPath)
	cmd.Env = append(isolatedGoRunEnv(t), "EMBER_TOKEN=wrong", "CONFIG_PATH=/nonexistent/awtrix.json")
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

func TestDoctorWarnIsNonFatal(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
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

func TestAdminDoctorWarnReturns200(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer awtrix.Close()

	app := newAppForDoctor(t, awtrix.URL)
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

func TestCheckMeetingsDisabledWithURLs(t *testing.T) {
	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{
		"http://calendar.example.com/feed1.ics",
		"http://calendar.example.com/feed2.ics",
	}

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

func TestCheckMeetingsStale(t *testing.T) {
	pub := &recordingPublisher{}
	app := newMeetingsTestApp(t, pub)
	app.meetingsURLs = []string{"http://calendar.example.com/feed.ics"}
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
	if !strings.Contains(got.Detail, "ago") {
		t.Errorf("detail should mention age ('ago'); got %q", got.Detail)
	}
}

func TestCheckAWTRIXReachable(t *testing.T) {
	var gotMethod, gotPath string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	check := func(base string) CheckResult {
		cfg := defaultConfig()
		cfg.AWTRIX.HTTPBaseURL = base
		return checkAWTRIXReachable(context.Background(), &cfg)
	}

	res := check(srv.URL)
	if res.Status != StatusOK || gotMethod != http.MethodGet || gotPath != "/api/v1/device" {
		t.Fatalf("200: status=%q request=%s %s", res.Status, gotMethod, gotPath)
	}
	if want := "GET " + srv.URL + "/api/v1/device → 200 ("; !strings.HasPrefix(res.Detail, want) {
		t.Fatalf("200 detail = %q, want prefix %q", res.Detail, want)
	}

	status = http.StatusNotFound
	if res := check(srv.URL); res.Status != StatusOK {
		t.Fatalf("404 is still a reachable clock, got %q (%s)", res.Status, res.Detail)
	}

	status = http.StatusServiceUnavailable
	if res := check(srv.URL); res.Status != StatusFail || !strings.Contains(res.Detail, "→ 503") {
		t.Fatalf("503: %q %q", res.Status, res.Detail)
	}

	if res := check(""); res.Status != StatusFail || res.Detail != "awtrix.http_base_url empty" {
		t.Fatalf("empty base: %q %q", res.Status, res.Detail)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	if res := check(deadURL); res.Status != StatusFail || !strings.HasPrefix(res.Detail, "GET "+deadURL+"/api/v1/device: ") {
		t.Fatalf("unreachable: %q %q", res.Status, res.Detail)
	}
}

func TestCheckCapabilitiesCountsNG12RTTTLAsBuzzer(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "")
	cases := []struct {
		name  string
		audio awtrix.AudioCaps
		want  string
	}{
		{"ng11 buzzer", awtrix.AudioCaps{Buzzer: true}, "buzzer=true"},
		{"ng12 rtttl", ng12Audio(true), "buzzer=true"},
		{"ng12 no rtttl", ng12Audio(false), "buzzer=false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
			withAudioCaps(app, c.audio)
			if got := checkCapabilities(app); got.Status != StatusOK || !strings.Contains(got.Detail, c.want) {
				t.Fatalf("check = %+v, want %s", got, c.want)
			}
		})
	}
}
