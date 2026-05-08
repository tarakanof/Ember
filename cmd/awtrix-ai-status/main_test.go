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

// helper
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
