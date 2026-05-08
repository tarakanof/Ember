package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
	if len(publisher.indicator) != 2 {
		t.Fatalf("indicator publishes = %d, want 2", len(publisher.indicator))
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
