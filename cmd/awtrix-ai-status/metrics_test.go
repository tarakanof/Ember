package main

import (
	"bytes"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMetrics_NilSafeIncrements(t *testing.T) {
	var m *metrics // explicitly nil — proves bare &App{} can't panic
	m.incRequest("/x", 200)
	m.incPublishOK()
	m.incPublishFail()
	m.incRateLimitDenied()
	m.incSessionEvicted()
	// no asserts — surviving the calls is the assertion
}

func TestMetrics_IncRequest_AggregatesPerKey(t *testing.T) {
	m := newMetrics()
	for i := 0; i < 5; i++ {
		m.incRequest("POST /v1/status", 200)
	}
	for i := 0; i < 3; i++ {
		m.incRequest("POST /v1/status", 400)
	}
	m.incRequest("GET /healthz", 200)

	// Read back via the underlying sync.Map.
	counts := map[requestKey]int64{}
	m.requestsTotal.Range(func(k, v any) bool {
		counts[k.(requestKey)] = loadAtomicInt(v)
		return true
	})
	if got := counts[requestKey{"POST /v1/status", 200}]; got != 5 {
		t.Errorf("POST /v1/status 200 = %d, want 5", got)
	}
	if got := counts[requestKey{"POST /v1/status", 400}]; got != 3 {
		t.Errorf("POST /v1/status 400 = %d, want 3", got)
	}
	if got := counts[requestKey{"GET /healthz", 200}]; got != 1 {
		t.Errorf("GET /healthz 200 = %d, want 1", got)
	}
}

func TestMetrics_IncRequest_UnmatchedFallback(t *testing.T) {
	m := newMetrics()
	m.incRequest("", 404)
	found := false
	m.requestsTotal.Range(func(k, _ any) bool {
		if k.(requestKey).pattern == "<unmatched>" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("empty pattern did not collapse to <unmatched>")
	}
}

func TestPromLabelValue_EscapesPerSpec(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`with "quotes"`, `with \"quotes\"`},
		{`back\slash`, `back\\slash`},
		{"line\nbreak", `line\nbreak`},
		{`mix"\` + "\n", `mix\"\\\n`},
	}
	for _, c := range cases {
		if got := promLabelValue(c.in); got != c.want {
			t.Errorf("promLabelValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// loadAtomicInt extracts the int64 from the *atomic.Int64 stored as the
// sync.Map value. Helper kept here so the test file owns the type assertion.
func loadAtomicInt(v any) int64 {
	type loader interface{ Load() int64 }
	return v.(loader).Load()
}

func TestMetrics_PublishCounters(t *testing.T) {
	m := newMetrics()
	for i := 0; i < 5; i++ {
		m.incPublishOK()
	}
	for i := 0; i < 3; i++ {
		m.incPublishFail()
	}
	if got := m.publishTotalOK.Load(); got != 5 {
		t.Errorf("publishTotalOK = %d, want 5", got)
	}
	if got := m.publishTotalFail.Load(); got != 3 {
		t.Errorf("publishTotalFail = %d, want 3", got)
	}
}

func TestMetrics_RateLimitAndSessionCounters(t *testing.T) {
	m := newMetrics()
	m.incRateLimitDenied()
	m.incRateLimitDenied()
	m.incSessionEvicted()
	if got := m.rateLimitDenied.Load(); got != 2 {
		t.Errorf("rateLimitDenied = %d, want 2", got)
	}
	if got := m.sessionsEvicted.Load(); got != 1 {
		t.Errorf("sessionsEvicted = %d, want 1", got)
	}
}

// Sanity: confirm escaper doesn't %q-escape non-ASCII bytes.
func TestPromLabelValue_NoSurprisingByteEscape(t *testing.T) {
	got := promLabelValue("emoji is fine: \xe2\x98\x83") // ☃
	if strings.Contains(got, `\x`) {
		t.Errorf("promLabelValue should not %%q-escape non-printables: %q", got)
	}
}

// newAppForMetrics returns an App constructed via NewApp (so metrics is
// populated) with sane defaults. Callers can reach in to set lastPublish*
// state and seed sessions before calling render.
func newAppForMetrics(t *testing.T) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return app
}

func TestRender_StructureAndHelpLines(t *testing.T) {
	app := newAppForMetrics(t)
	app.metrics.incRequest("POST /v1/status", 200)
	app.metrics.incRequest("POST /v1/status", 200)
	app.metrics.incRequest("POST /v1/status", 400)
	app.metrics.incPublishOK()
	app.metrics.incPublishFail()
	app.metrics.incRateLimitDenied()
	app.metrics.incSessionEvicted()

	var buf bytes.Buffer
	app.metrics.render(&buf, app)
	body := buf.String()

	for _, name := range []string{
		"awtrix_requests_total",
		"awtrix_publish_total",
		"awtrix_rate_limit_denied_total",
		"awtrix_sessions_evicted_total",
		"awtrix_sessions_active",
		"awtrix_uptime_seconds",
		"awtrix_last_publish_unix",
		"awtrix_last_publish_ok",
		"awtrix_ratelimit_buckets",
		"awtrix_build_info",
	} {
		if !strings.Contains(body, "# HELP "+name+" ") {
			t.Errorf("missing HELP line for %s", name)
		}
		if !strings.Contains(body, "# TYPE "+name+" ") {
			t.Errorf("missing TYPE line for %s", name)
		}
	}

	if !strings.Contains(body, `awtrix_requests_total{pattern="POST /v1/status",status="200"} 2`) {
		t.Errorf("body missing the 200-counter line:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_requests_total{pattern="POST /v1/status",status="400"} 1`) {
		t.Errorf("body missing the 400-counter line:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_publish_total{result="ok"} 1`) {
		t.Errorf("body missing publish ok line:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_publish_total{result="fail"} 1`) {
		t.Errorf("body missing publish fail line:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_rate_limit_denied_total 1`) {
		t.Errorf("body missing rate_limit_denied line:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_sessions_evicted_total 1`) {
		t.Errorf("body missing sessions_evicted line:\n%s", body)
	}
}

func TestRender_GaugesReadFromApp(t *testing.T) {
	app := newAppForMetrics(t)

	app.mu.Lock()
	app.sessions["a/claude/s1"] = Session{Source: "a", Tool: "claude", Session: "s1", State: "running", UpdatedAt: time.Now()}
	app.sessions["a/claude/s2"] = Session{Source: "a", Tool: "claude", Session: "s2", State: "waiting", UpdatedAt: time.Now()}
	publishTime := time.Unix(1_700_000_000, 0)
	app.lastPublishAt = publishTime
	app.lastPublishOK = true
	app.mu.Unlock()

	app.limiter.mu.Lock()
	app.limiter.buckets["192.0.2.1"] = &ipBucket{}
	app.limiter.mu.Unlock()

	var buf bytes.Buffer
	app.metrics.render(&buf, app)
	body := buf.String()
	if !strings.Contains(body, "awtrix_sessions_active 2") {
		t.Errorf("sessions_active wrong:\n%s", body)
	}
	if !strings.Contains(body, "awtrix_last_publish_ok 1") {
		t.Errorf("last_publish_ok wrong:\n%s", body)
	}
	if !strings.Contains(body, "awtrix_last_publish_unix 1700000000") {
		t.Errorf("last_publish_unix wrong:\n%s", body)
	}
	if !strings.Contains(body, "awtrix_ratelimit_buckets 1") {
		t.Errorf("ratelimit_buckets wrong:\n%s", body)
	}
	upRe := regexp.MustCompile(`awtrix_uptime_seconds (\d+\.\d+)`)
	if !upRe.MatchString(body) {
		t.Errorf("uptime_seconds gauge missing:\n%s", body)
	}
	if !strings.Contains(body, `awtrix_build_info{revision=`) {
		t.Errorf("build_info line missing labels:\n%s", body)
	}
	if !strings.Contains(body, `,go_version=`) {
		t.Errorf("build_info line missing go_version label:\n%s", body)
	}
}

func TestRender_LastPublishUnixZeroWhenNoPublish(t *testing.T) {
	app := newAppForMetrics(t)
	var buf bytes.Buffer
	app.metrics.render(&buf, app)
	if !strings.Contains(buf.String(), "awtrix_last_publish_unix 0") {
		t.Errorf("body should show last_publish_unix 0 before any publish:\n%s", buf.String())
	}
}
