package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// stateSeed is one session in a /state golden case, aged relative to "now".
type stateSeed struct {
	s   Session
	age time.Duration
}

func ptr[T any](v T) *T { return &v }

// stateGoldenCases pin the GET /state body byte for byte (timestamps masked):
// session order, the legacy Render label, colours and counters, and which
// sessions the per-state staleness policy (stale 25s, done TTL 30s) drops.
var stateGoldenCases = []struct {
	name  string
	seeds []stateSeed
}{
	{"empty", nil},
	{"idle-only", []stateSeed{
		{Session{Source: "m4", Tool: "claude", Session: "a", State: "idle"}, 1 * time.Second},
	}},
	{"waiting-wins", []stateSeed{
		{Session{Source: "m4", Tool: "codex", Session: "r", State: "running", Activity: "go test", ContextPct: ptr(42), SourceColor: ptr("#12ab34"), RateWindowPct: ptr(17), TokensToday: 1234}, 2 * time.Second},
		{Session{Source: "m5", Tool: "claude", Session: "w", State: "waiting", Message: "approve Bash"}, 5 * time.Second},
		{Session{Source: "m4", Tool: "claude", Session: "d", State: "done", Message: "build green"}, 9 * time.Second},
		{Session{Source: "m4", Tool: "claude", Session: "stale", State: "running"}, 26 * time.Second},
		{Session{Source: "m4", Tool: "claude", Session: "old-done", State: "done"}, 31 * time.Second},
		{Session{Source: "m5", Tool: "claude", Session: "lingering-error", State: "error", Message: "boom"}, 28 * time.Second},
	}},
	{"error-wins", []stateSeed{
		{Session{Source: "m4", Tool: "gemini", Session: "e", State: "error"}, 3 * time.Second},
		{Session{Source: "m4", Tool: "claude", Session: "r", State: "running"}, 1 * time.Second},
	}},
	{"aggregate", []stateSeed{
		{Session{Source: "a", Tool: "claude", Session: "1", State: "running"}, 1 * time.Second},
		{Session{Source: "b", Tool: "codex", Session: "2", State: "running"}, 4 * time.Second},
		{Session{Source: "c", Tool: "claude", Session: "3", State: "done"}, 6 * time.Second},
		{Session{Source: "d", Tool: "claude", Session: "4", State: "idle"}, 7 * time.Second},
	}},
	{"done-alone", []stateSeed{
		{Session{Source: "a", Tool: "claude", Session: "1", State: "done", Message: "build green"}, 10 * time.Second},
	}},
}

var stateTimestamp = regexp.MustCompile(`"(now|updated_at)":"[^"]*"`)

func TestStateGolden(t *testing.T) {
	for _, tc := range stateGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Display.StaleSeconds = 25
			cfg.Display.DoneTTLSeconds = 30
			app, srv := newTestServer(t, cfg)
			seedState(t, app, tc.seeds)

			resp, err := http.Get(srv.URL + "/state")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			got := stateTimestamp.ReplaceAll(body, []byte(`"$1":"<T>"`))

			path := filepath.Join("testdata", "state", tc.name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run go test -run TestStateGolden -update): %v", err)
			}
			if string(got) != string(want) {
				t.Fatalf("/state drifted from %s\n got: %s\nwant: %s", path, got, want)
			}
		})
	}
}

// seedState puts each seed into the app's session map aged by seed.age.
func seedState(t *testing.T, app *App, seeds []stateSeed) {
	t.Helper()
	now := time.Now()
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, sd := range seeds {
		s := sd.s
		s.UpdatedAt = now.Add(-sd.age)
		app.sessions[s.Key()] = s
	}
}
