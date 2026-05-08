package main

import (
	"context"
	"io"
	"log/slog"
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
