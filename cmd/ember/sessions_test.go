package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// withSessionClock swaps app's session registry for one on a fake clock.
func withSessionClock(app *App) *fakeClock {
	clk := newFakeClock()
	app.sessions = app.newSessionRegistry(clk.Now)
	return clk
}

func setClock(clk *fakeClock, at time.Time) {
	clk.mu.Lock()
	clk.now = at
	clk.mu.Unlock()
}

func sessionKeys(snap Snapshot) map[string]bool {
	out := make(map[string]bool, len(snap.Sessions))
	for _, s := range snap.Sessions {
		out[s.Key()] = true
	}
	return out
}

// The registry's policy comes from the live display config: stale_seconds for
// active states, done_ttl_seconds for done/error. The per-state boundaries
// are covered in internal/sessions; this pins the wiring.
func TestSessionPolicyFollowsDisplayConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	cfg.Display.DoneTTLSeconds = 30
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	clk := withSessionClock(app)

	app.Upsert(StatusRequest{Source: "src", Tool: "claude", Session: "run", State: "running"})
	app.Upsert(StatusRequest{Source: "src", Tool: "claude", Session: "done", State: "done"})
	clk.Advance(28 * time.Second)

	got := sessionKeys(app.Snapshot())
	if got["src/claude/run"] {
		t.Errorf("running at 28s should be reaped (stale_seconds=25)")
	}
	if !got["src/claude/done"] {
		t.Errorf("done at 28s should linger (done_ttl_seconds=30)")
	}

	// A hot-reloaded config applies on the next access.
	app.updateConfig(func(c *Config) { c.Display.DoneTTLSeconds = 20 })
	if sessionKeys(app.Snapshot())["src/claude/done"] {
		t.Errorf("done at 28s should be reaped once done_ttl_seconds drops to 20")
	}
}

// Reaping is logged and counted whatever triggers it, not only a /state render.
func TestSessionReapIsCountedWithoutRender(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	clk := withSessionClock(app)

	app.Upsert(StatusRequest{Source: "src", Tool: "claude", Session: "x", State: "running"})
	clk.Advance(26 * time.Second)
	app.Delete("unrelated/claude/key")

	if got := app.metrics.sessionsEvicted.Load(); got != 1 {
		t.Fatalf("sessionsEvicted = %d, want 1", got)
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

func TestCompactTextTruncatesByRune(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"short ascii", "  a   b  ", "a b"},
		{"ascii at 80", strings.Repeat("a", 80), strings.Repeat("a", 80)},
		{"ascii over 80", strings.Repeat("a", 81), strings.Repeat("a", 77) + "..."},
		// 60 Cyrillic runes are 120 bytes: under the 80-character cap, kept whole.
		{"cyrillic within 80 runes", strings.Repeat("ж", 60), strings.Repeat("ж", 60)},
		{"cyrillic over 80 runes", strings.Repeat("ж", 90), strings.Repeat("ж", 77) + "..."},
		{"emoji over 80 runes", strings.Repeat("🔥", 81), strings.Repeat("🔥", 77) + "..."},
		// Rune 77 is a combining acute: the cut backs off to keep "é" whole.
		{"combining mark at the cut", strings.Repeat("a", 76) + "é" + strings.Repeat("b", 10), strings.Repeat("a", 76) + "..."},
		// A ZWJ family (man ZWJ woman) straddling the cut is dropped whole.
		{"zwj sequence at the cut", strings.Repeat("a", 76) + "👨‍👩" + strings.Repeat("b", 10), strings.Repeat("a", 76) + "..."},
		{"skin tone at the cut", strings.Repeat("a", 76) + "👍🏽" + strings.Repeat("b", 10), strings.Repeat("a", 76) + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compactText(tc.in)
			if got != tc.want {
				t.Fatalf("compactText = %q, want %q", got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("compactText produced invalid UTF-8: %q", got)
			}
		})
	}
}

func TestWaitingRenderKeepsMultibyteMessageValid(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	msg := strings.Repeat("проверка ", 12) // 108 runes, 204 bytes
	render, _ := app.Upsert(StatusRequest{Source: "a", Tool: "claude", Session: "1", State: "waiting", Message: msg})
	if !utf8.ValidString(render.Text) {
		t.Fatalf("Text is not valid UTF-8: %q", render.Text)
	}
	if n := utf8.RuneCountInString(render.Text); n != 80 {
		t.Fatalf("Text is %d runes, want 80 (77 + ...): %q", n, render.Text)
	}
}

func TestLabelForCapitalisesMultibyteTool(t *testing.T) {
	if got := labelFor(Session{Tool: "ёж"}); got != "Ёж" {
		t.Fatalf("labelFor = %q, want %q", got, "Ёж")
	}
	if got := labelFor(Session{Tool: "gemini"}); got != "Gemini" {
		t.Fatalf("labelFor = %q, want Gemini", got)
	}
}
