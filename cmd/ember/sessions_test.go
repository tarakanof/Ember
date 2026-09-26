package main

import (
	"testing"
	"time"
)

func TestWaitingStatusWinsOverRunningStatus(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "codex",
		Session: "repo",
		State:   "running",
	})
	render, _ := app.Upsert(StatusRequest{
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
	render, _ := app.Upsert(StatusRequest{
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
	render, _ := app.Upsert(StatusRequest{
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
	render, _ := app.Upsert(StatusRequest{Source: "b", Tool: "claude", Session: "2", State: "waiting"})
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
	render, _ := app.Upsert(StatusRequest{Source: "e", Tool: "claude", Session: "5", State: "running"})
	if !contains(render.Text, "W2") || !contains(render.Text, "R3") {
		t.Errorf("Text = %q, want aggregate AI W2 R3", render.Text)
	}
}
