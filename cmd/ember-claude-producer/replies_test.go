package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type replySink struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
	got    []producer.Reply
}

func newReplySink(t *testing.T) *replySink {
	t.Helper()
	s := &replySink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.bodies = append(s.bodies, r.Method+" "+r.URL.Path+" "+string(b))
		if r.URL.Path == "/v1/day/replies" {
			var body struct {
				Replies []producer.Reply `json:"replies"`
			}
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("replies body: %v", err)
			}
			s.got = append(s.got, body.Replies...)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *replySink) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.bodies, "\n")
}

func copyBackfillFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i, sub := range []string{"-Users-dt-Github-Widget", "-Users-dt-Github-Widget-fork"} {
		src := filepath.Join("testdata", "backfill", sub)
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dst, sub, e.Name())
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			mod := base.Add(time.Duration(i) * time.Minute)
			if err := os.Chtimes(p, mod, mod); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dst
}

func TestBackfillPostsOnlyHumanPromptsOnce(t *testing.T) {
	sink := newReplySink(t)
	cfg := Config{}
	cfg.Common = producer.DefaultCommon()
	cfg.Source, cfg.ServerURL = "m4", sink.srv.URL
	dir := copyBackfillFixture(t)
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	client := producer.NewClient(cfg.ServerURL, "tok", 5*time.Second)
	if err := backfill(context.Background(), cfg, dir, since, client, &out); err != nil {
		t.Fatal(err)
	}
	at := func(s string) int64 { tm, _ := time.Parse(time.RFC3339, s); return tm.Unix() }
	want := []producer.Reply{
		{At: at("2026-10-20T09:15:00Z"), Source: "m4", Tool: "claude", Session: "11111111-1111-4111-8111-111111111111", Repo: "Widget"},
		{At: at("2026-10-20T09:22:01Z"), Source: "m4", Tool: "claude", Session: "11111111-1111-4111-8111-111111111111", Repo: "Widget"},
		{At: at("2026-10-20T10:00:00Z"), Source: "m4", Tool: "claude", Session: "11111111-1111-4111-8111-111111111111", Repo: "Widget"},
		{At: at("2026-10-21T08:00:00Z"), Source: "m4", Tool: "claude", Session: "55555555-5555-4555-8555-555555555555", Repo: "Widget"},
		{At: at("2026-10-22T09:00:00Z"), Source: "m4", Tool: "claude", Session: "66666666-6666-4666-8666-666666666666", Repo: "Widget"},
	}
	if len(sink.got) != len(want) {
		t.Fatalf("replies = %+v, want %+v", sink.got, want)
	}
	for i := range want {
		if sink.got[i] != want[i] {
			t.Errorf("reply %d = %+v, want %+v", i, sink.got[i], want[i])
		}
	}
	if !strings.Contains(out.String(), "posted 5 prompts from 2 transcripts") {
		t.Errorf("output = %q", out.String())
	}
	noPromptText(t, sink.all()+out.String())

	sink.got = nil
	if err := backfill(context.Background(), cfg, dir, since, client, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != len(want) || sink.got[0] != want[0] {
		t.Fatalf("a rerun posted %+v, want the same replies for the server to ignore", sink.got)
	}
}

func TestBackfillWithoutProjectsSendsNoRepo(t *testing.T) {
	sink := newReplySink(t)
	cfg := Config{}
	cfg.Common = producer.DefaultCommon()
	cfg.Source, cfg.ServerURL, cfg.SendProject = "m4", sink.srv.URL, false
	client := producer.NewClient(cfg.ServerURL, "tok", 5*time.Second)
	if err := backfill(context.Background(), cfg, copyBackfillFixture(t), time.Time{}, client, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, r := range sink.got {
		if r.Repo != "" {
			t.Fatalf("reply %+v carries a repo with send_project off", r)
		}
	}
}

func TestBackfillDryRunPostsNothing(t *testing.T) {
	cfg := Config{}
	cfg.Common = producer.DefaultCommon()
	cfg.Source = "m4"
	var out bytes.Buffer
	if err := backfill(context.Background(), cfg, copyBackfillFixture(t), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "5 prompts in 2 transcripts") {
		t.Errorf("output = %q", out.String())
	}
}

func noPromptText(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, "PROMPT") || strings.Contains(s, "secret prompt") {
		t.Fatalf("prompt text leaked: %s", s)
	}
}

func TestHookPostsOneExactReplyForAHumanPrompt(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		prompt  string
		replies int
	}{
		{"cli", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"}, "secret prompt", 1},
		{"desktop", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, "secret prompt", 1},
		{"sdk child of a cli session", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli", "CLAUDE_AGENT_SDK_VERSION": "0.3.276"}, "secret prompt", 0},
		{"T3 or Agent SDK", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-ts"}, "secret prompt", 0},
		{"SDK review", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-py"}, "secret prompt", 0},
		{"claude -p title", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}, "secret prompt", 0},
		{"task notification", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"}, "<task-notification>secret prompt", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHookHarness(t)
			promptEnv = func(k string) string { return c.env[k] }
			t.Cleanup(func() { promptEnv = func(string) string { return "" } })
			cwd := filepath.Join(t.TempDir(), "Gizmo")
			body, _ := json.Marshal(hookInput{HookEventName: "UserPromptSubmit", SessionID: "abc", CWD: cwd, Prompt: c.prompt})
			dispatchHookForTest(t, "user-prompt-submit", body)
			n := 0
			for _, b := range *h.bodies {
				if strings.Contains(b, `"replies"`) {
					n++
					var got struct {
						Replies []producer.Reply `json:"replies"`
					}
					if err := json.Unmarshal([]byte(b), &got); err != nil {
						t.Fatal(err)
					}
					r := got.Replies[0]
					if len(got.Replies) != 1 || r.Source != "test-mbp" || r.Tool != "claude" || r.Session != "abc" || r.Repo != "Gizmo" || r.At == 0 {
						t.Errorf("reply = %+v", got.Replies)
					}
					noPromptText(t, b)
				}
			}
			if n != c.replies {
				t.Fatalf("reply posts = %d, want %d", n, c.replies)
			}
		})
	}
}
