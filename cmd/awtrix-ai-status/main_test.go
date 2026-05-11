package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recordingPublisher struct {
	customApps []map[string]any
	notify     []map[string]any
	indicator  []map[string]any
}

func (p *recordingPublisher) CustomApp(_ context.Context, _ string, payload map[string]any) error {
	p.customApps = append(p.customApps, payload)
	return nil
}

func (p *recordingPublisher) Notify(_ context.Context, payload map[string]any) error {
	p.notify = append(p.notify, payload)
	return nil
}

func (p *recordingPublisher) Indicator(_ context.Context, _ int, payload map[string]any) error {
	p.indicator = append(p.indicator, payload)
	return nil
}

func TestStatusRequestNormalizesDefaults(t *testing.T) {
	session := StatusRequest{}.normalized()

	if session.Source != "unknown" {
		t.Fatalf("Source = %q, want unknown", session.Source)
	}
	if session.Tool != "ai" {
		t.Fatalf("Tool = %q, want ai", session.Tool)
	}
	if session.Session != "default" {
		t.Fatalf("Session = %q, want default", session.Session)
	}
	if session.State != "idle" {
		t.Fatalf("State = %q, want idle (empty input should default)", session.State)
	}
}

func TestWaitingStatusWinsOverRunningStatus(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "codex",
		Session: "repo",
		State:   "running",
	})
	render := app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "claude",
		Session: "desktop",
		State:   "waiting",
		Message: "approve Bash",
	})

	if render.Text != "WAIT approve Bash" {
		t.Fatalf("Text = %q, want WAIT approve Bash", render.Text)
	}
	if render.Waiting != 1 {
		t.Fatalf("Waiting = %d, want 1", render.Waiting)
	}
	if render.Running != 1 {
		t.Fatalf("Running = %d, want 1", render.Running)
	}
}

func TestPublishWritesCustomAppAndIndicators(t *testing.T) {
	publisher := &recordingPublisher{}
	app := NewApp(defaultConfig(), publisher, testLogger())
	app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "codex",
		Session: "repo",
		State:   "running",
	})

	if err := app.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(publisher.customApps) != 1 {
		t.Fatalf("custom app publishes = %d, want 1", len(publisher.customApps))
	}
	if got := publisher.customApps[0]["text"]; got != "Codex run" {
		t.Fatalf("custom app text = %v, want Codex run", got)
	}
	if len(publisher.indicator) != 3 {
		t.Fatalf("indicator publishes = %d, want 3", len(publisher.indicator))
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T, cfg Config) (*App, *httptest.Server) {
	t.Helper()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return app, srv
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPostStatusRejectsMissingFields(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty source", map[string]any{"source": "", "tool": "claude", "session": "x", "state": "running"}},
		{"empty tool", map[string]any{"source": "dt-mbp", "tool": "", "session": "x", "state": "running"}},
		{"empty session", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "", "state": "running"}},
		{"empty state", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "x", "state": ""}},
		{"unknown state", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "x", "state": "potato"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, srv, "/v1/status", c.body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestPostStatusAcceptsValidRequest(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source":  "dt-mbp",
		"tool":    "claude",
		"session": "awtrix",
		"state":   "running",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDefaultConfigTimingValues(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Display.StaleSeconds != 25 {
		t.Errorf("StaleSeconds = %d, want 25", cfg.Display.StaleSeconds)
	}
	if cfg.Display.DoneTTLSeconds != 30 {
		t.Errorf("DoneTTLSeconds = %d, want 30", cfg.Display.DoneTTLSeconds)
	}
	if cfg.Display.HeartbeatSeconds != 10 {
		t.Errorf("HeartbeatSeconds = %d, want 10", cfg.Display.HeartbeatSeconds)
	}
}

func TestPerStateStalenessReapsActiveSessions(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	cfg.Display.DoneTTLSeconds = 30
	app := NewApp(cfg, &recordingPublisher{}, testLogger())

	// Inject a running session that's 26s old — should be reaped.
	app.sessions["src/claude/x"] = Session{
		Source: "src", Tool: "claude", Session: "x",
		State:     "running",
		UpdatedAt: time.Now().Add(-26 * time.Second),
	}
	// Inject a running session 24s old — should survive.
	app.sessions["src/claude/y"] = Session{
		Source: "src", Tool: "claude", Session: "y",
		State:     "running",
		UpdatedAt: time.Now().Add(-24 * time.Second),
	}
	app.Snapshot() // triggers reaping

	if _, ok := app.sessions["src/claude/x"]; ok {
		t.Errorf("expected src/claude/x to be reaped (26s > stale_seconds=25)")
	}
	if _, ok := app.sessions["src/claude/y"]; !ok {
		t.Errorf("expected src/claude/y to survive (24s <= stale_seconds=25)")
	}
}

func TestPerStateStalenessLingersDoneAndError(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	cfg.Display.DoneTTLSeconds = 30
	app := NewApp(cfg, &recordingPublisher{}, testLogger())

	// Done session 28s old (over StaleSeconds, under DoneTTL) — should survive.
	app.sessions["src/claude/d"] = Session{
		Source: "src", Tool: "claude", Session: "d",
		State:     "done",
		UpdatedAt: time.Now().Add(-28 * time.Second),
	}
	// Done session 31s old — over DoneTTL — should be reaped.
	app.sessions["src/claude/e"] = Session{
		Source: "src", Tool: "claude", Session: "e",
		State:     "done",
		UpdatedAt: time.Now().Add(-31 * time.Second),
	}
	// Error session 28s old — should survive (uses DoneTTL).
	app.sessions["src/claude/f"] = Session{
		Source: "src", Tool: "claude", Session: "f",
		State:     "error",
		UpdatedAt: time.Now().Add(-28 * time.Second),
	}
	app.Snapshot()

	if _, ok := app.sessions["src/claude/d"]; !ok {
		t.Errorf("done at 28s should linger (DoneTTL=30)")
	}
	if _, ok := app.sessions["src/claude/e"]; ok {
		t.Errorf("done at 31s should be reaped (DoneTTL=30)")
	}
	if _, ok := app.sessions["src/claude/f"]; !ok {
		t.Errorf("error at 28s should linger (DoneTTL=30)")
	}
}

func TestRenderDoneLingersWhenAlone(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	render := app.Upsert(StatusRequest{
		Source: "dt-mbp", Tool: "claude", Session: "x",
		State: "done", Message: "build green",
	})
	if render.Done != 1 {
		t.Errorf("Done = %d, want 1", render.Done)
	}
	// Per-session label for single-session done group:
	if !contains(render.Text, "build green") && !contains(render.Text, "done") {
		t.Errorf("Text = %q, want a per-session done label", render.Text)
	}
	if render.Color != "#707070" {
		t.Errorf("Color = %q, want grey #707070", render.Color)
	}
}

func TestRenderIdleSessionNeverWins(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	render := app.Upsert(StatusRequest{
		Source: "dt-mbp", Tool: "claude", Session: "x",
		State: "idle",
	})
	if render.Text != "AI idle" {
		t.Errorf("Text = %q, want AI idle (idle never wins)", render.Text)
	}
}

func TestRenderAggregateLabelForMultipleWaiting(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{Source: "a", Tool: "claude", Session: "1", State: "waiting"})
	render := app.Upsert(StatusRequest{Source: "b", Tool: "claude", Session: "2", State: "waiting"})
	if render.Waiting != 2 {
		t.Errorf("Waiting = %d, want 2", render.Waiting)
	}
	if !contains(render.Text, "W2") {
		t.Errorf("Text = %q, want aggregate including W2", render.Text)
	}
}

func TestRenderAggregateMixedGroups(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{Source: "a", Tool: "claude", Session: "1", State: "waiting"})
	app.Upsert(StatusRequest{Source: "b", Tool: "claude", Session: "2", State: "waiting"})
	app.Upsert(StatusRequest{Source: "c", Tool: "claude", Session: "3", State: "running"})
	app.Upsert(StatusRequest{Source: "d", Tool: "claude", Session: "4", State: "running"})
	render := app.Upsert(StatusRequest{Source: "e", Tool: "claude", Session: "5", State: "running"})
	if !contains(render.Text, "W2") || !contains(render.Text, "R3") {
		t.Errorf("Text = %q, want aggregate AI W2 R3", render.Text)
	}
}

func TestPublishLightsIndicator3WhenDoneOrErrorPresent(t *testing.T) {
	publisher := &recordingPublisher{}
	app := NewApp(defaultConfig(), publisher, testLogger())
	app.Upsert(StatusRequest{Source: "x", Tool: "claude", Session: "1", State: "done"})

	if err := app.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(publisher.indicator) != 3 {
		t.Fatalf("indicator count = %d, want 3", len(publisher.indicator))
	}
	ind3 := publisher.indicator[2]
	if ind3["color"] == "0" || ind3["color"] == 0 {
		t.Errorf("indicator 3 color = %v, want lit grey when done present", ind3["color"])
	}
}

func TestPublishIndicator3OffWhenNoLinger(t *testing.T) {
	publisher := &recordingPublisher{}
	app := NewApp(defaultConfig(), publisher, testLogger())
	app.Upsert(StatusRequest{Source: "x", Tool: "claude", Session: "1", State: "running"})

	if err := app.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}

	ind3 := publisher.indicator[2]
	if c, _ := ind3["color"].(string); c != "0" {
		t.Errorf("indicator 3 color = %v, want \"0\" (off) when no done/error", ind3["color"])
	}
}

func TestDeleteStatusRemovesSession(t *testing.T) {
	app, srv := newTestServer(t, defaultConfig())
	app.Upsert(StatusRequest{Source: "dt-mbp", Tool: "claude", Session: "x", State: "running"})

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"dt-mbp","tool":"claude","session":"x"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, ok := app.sessions["dt-mbp/claude/x"]; ok {
		t.Errorf("session not deleted")
	}
}

func TestDeleteStatusIdempotent(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"a","tool":"b","session":"c"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestDeleteStatusRejectsEmptyKey(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"","tool":"b","session":"c"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPostStatusRejectsOversizedBody(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())

	huge := strings.Repeat("x", (1<<20)+1) // 1 MiB + 1 byte
	body := map[string]any{
		"source":  "dt-mbp",
		"tool":    "claude",
		"session": "x",
		"state":   "running",
		"message": huge,
	}
	resp := postJSON(t, srv, "/v1/status", body, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

// helper
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func newTestServerWithToken(t *testing.T, token string) (*App, *httptest.Server) {
	t.Helper()
	cfg := defaultConfig()
	cfg.Auth.StatusToken = token
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return app, srv
}

func TestAuthRequiredOnWriteEndpoints(t *testing.T) {
	_, srv := newTestServerWithToken(t, "secret-token")

	// No auth header
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", resp.StatusCode)
	}

	// Wrong token
	resp = postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, map[string]string{"Authorization": "Bearer wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", resp.StatusCode)
	}

	// Correct token
	resp = postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, map[string]string{"Authorization": "Bearer secret-token"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthDisabledWhenTokenEmpty(t *testing.T) {
	_, srv := newTestServerWithToken(t, "")
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("empty token / no auth: status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthNotRequiredOnReadEndpoints(t *testing.T) {
	_, srv := newTestServerWithToken(t, "secret-token")
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/state without auth: status = %d, want 200", resp.StatusCode)
	}

	resp, err = client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz without auth: status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireAuth_TokenRotation(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = "old"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := NewApp(cfg, &noopPublisher{}, logger)

	handler := requireAuth(app, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Old token works.
	req1, _ := http.NewRequest("GET", srv.URL, nil)
	req1.Header.Set("Authorization", "Bearer old")
	resp1, err := srv.Client().Do(req1)
	if err != nil {
		t.Fatalf("old token request: %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("old token: got %d, want 200", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Rotate.
	newCfg := *app.cfg.Load()
	newCfg.Auth.StatusToken = "new"
	app.cfg.Store(&newCfg)

	// Old now rejected.
	req2, _ := http.NewRequest("GET", srv.URL, nil)
	req2.Header.Set("Authorization", "Bearer old")
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("old-after-rotation request: %v", err)
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after rotation: got %d, want 401", resp2.StatusCode)
	}
	resp2.Body.Close()

	// New accepted.
	req3, _ := http.NewRequest("GET", srv.URL, nil)
	req3.Header.Set("Authorization", "Bearer new")
	resp3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatalf("new token request: %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("new token: got %d, want 200", resp3.StatusCode)
	}
	resp3.Body.Close()
}

type noopPublisher struct{}

func (noopPublisher) CustomApp(context.Context, string, map[string]any) error { return nil }
func (noopPublisher) Notify(context.Context, map[string]any) error            { return nil }
func (noopPublisher) Indicator(context.Context, int, map[string]any) error    { return nil }

func TestHTTPPublisher_BaseURLReloadable(t *testing.T) {
	hits1, hits2 := 0, 0
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits1++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv1.URL
	cfg.applyDefaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub, err := NewHTTPPublisher() // app filled below
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, logger)
	pub.app = app

	if err := pub.Notify(context.Background(), map[string]any{"text": "x"}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if hits1 != 1 || hits2 != 0 {
		t.Errorf("after publish 1: hits1=%d hits2=%d, want 1/0", hits1, hits2)
	}

	// Swap cfg to point at srv2.
	newCfg := *app.cfg.Load()
	newCfg.AWTRIX.HTTPBaseURL = srv2.URL
	app.cfg.Store(&newCfg)

	if err := pub.Notify(context.Background(), map[string]any{"text": "y"}); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if hits1 != 1 || hits2 != 1 {
		t.Errorf("after publish 2: hits1=%d hits2=%d, want 1/1", hits1, hits2)
	}
}

func TestApp_PublishUpdatesLastPublishFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.applyDefaults()

	pub, err := NewHTTPPublisher()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// pub.app is wired by NewApp; no manual setting needed.

	if err := app.Publish(context.Background()); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.lastPublishAt.IsZero() {
		t.Error("lastPublishAt not updated")
	}
	if !app.lastPublishOK {
		t.Errorf("lastPublishOK = false, want true (err=%q)", app.lastPublishErr)
	}
	if app.lastPublishErr != "" {
		t.Errorf("lastPublishErr = %q, want empty", app.lastPublishErr)
	}
}

func TestHandleStatus_413OnOversizeBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	body := bytes.Repeat([]byte("x"), (1<<20)+100)
	req, err := http.NewRequest("POST", srv.URL+"/v1/status", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized body: code = %d, want 413 or 400", resp.StatusCode)
	}
}

func TestHandleClear_413OnOversizeBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	body := bytes.Repeat([]byte("x"), 2048)
	req, err := http.NewRequest("POST", srv.URL+"/v1/clear", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized /v1/clear body: code = %d, want 413 or 400", resp.StatusCode)
	}
}

func TestDecodeJSON_RejectsTrailingValue(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, discardLogger())

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// Two top-level JSON values back-to-back.
	body := `{"source":"a","tool":"t","session":"s","state":"running"}{"x":1}`
	req, err := http.NewRequest("POST", srv.URL+"/v1/status", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("trailing value: code = %d, want 400", resp.StatusCode)
	}
}

// captureLogger returns a logger that writes JSON-formatted entries to
// the provided buffer at Debug level (so all Info entries are captured).
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestHandleStatus_LogsInfoOnParseFailure(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, captureLogger(&buf))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/status", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"level":"INFO"`) ||
		!strings.Contains(logs, `"msg":"request rejected"`) ||
		!strings.Contains(logs, `"reason":"parse"`) {
		t.Errorf("expected Info request-rejected reason=parse log, got: %s", logs)
	}
}

func TestHandleStatus_LogsInfoOnValidationFailure(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, captureLogger(&buf))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	body := `{"source":"","tool":"t","session":"s","state":"running"}`
	resp, err := http.Post(srv.URL+"/v1/status", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"validation"`) {
		t.Errorf("expected reason=validation log, got: %s", logs)
	}
}

func TestHandleNotify_LogsInfoOnEmptyText(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, captureLogger(&buf))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	body := `{"text":""}`
	resp, err := http.Post(srv.URL+"/v1/notify", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"validation"`) ||
		!strings.Contains(logs, `"field":"text"`) {
		t.Errorf("expected reason=validation field=text log, got: %s", logs)
	}
}

func TestStatusRequestValidate_OptionalFields(t *testing.T) {
	mk := func(ctxPct *int, srcColor *string) StatusRequest {
		return StatusRequest{
			Source: "a", Tool: "b", Session: "c", State: "running",
			ContextPct: ctxPct, SourceColor: srcColor,
		}
	}
	good := []StatusRequest{
		mk(nil, nil),
		mk(intPtr(0), nil),
		mk(intPtr(100), nil),
		mk(nil, strPtr("#aabbcc")),
		mk(intPtr(50), strPtr("#AABBCC")),
	}
	for _, r := range good {
		if err := r.validate(); err != nil {
			t.Errorf("validate(%+v) = %v, want nil", r, err)
		}
	}
	bad := []struct {
		name string
		r    StatusRequest
	}{
		{"ctx<0", mk(intPtr(-1), nil)},
		{"ctx>100", mk(intPtr(101), nil)},
		{"color missing #", mk(nil, strPtr("aabbcc"))},
		{"color short", mk(nil, strPtr("#aabb"))},
		{"color non-hex", mk(nil, strPtr("#xxxxxx"))},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.r.validate(); err == nil {
				t.Errorf("validate(%+v) = nil, want error", tc.r)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
