package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func tuiFixture(t *testing.T, originator, source string) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/rollout-tui-0.163.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var meta map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &meta); err != nil {
		t.Fatal(err)
	}
	p := meta["payload"].(map[string]any)
	p["originator"], p["source"] = originator, source
	first, _ := json.Marshal(meta)
	lines[0] = string(first)
	return lines
}

type codexReplySink struct {
	mu     sync.Mutex
	status int
	raw    []string
	got    []producer.Reply
}

func (s *codexReplySink) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/v1/day/replies" {
			return
		}
		s.raw = append(s.raw, string(b))
		if s.status != 0 {
			w.WriteHeader(s.status)
			return
		}
		var body struct {
			Replies []producer.Reply `json:"replies"`
		}
		_ = json.Unmarshal(b, &body)
		s.got = append(s.got, body.Replies...)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCodexTUIPromptsBecomeExactReplies(t *testing.T) {
	sink := &codexReplySink{}
	srv := sink.server(t)
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-tui.jsonl", now, tuiFixture(t, "codex-tui", "cli")...)
	w := newTestWatcher(dir, now)
	w.cfg.SendProject = true
	client := producer.NewClient(srv.URL, "", time.Second)
	runOnce(context.Background(), w, nil, client)

	stamp := func(s string) int64 { tm, _ := time.Parse(time.RFC3339Nano, s); return tm.Unix() }
	if len(sink.got) != 2 {
		t.Fatalf("replies = %+v, want the 2 typed prompts", sink.got)
	}
	want := producer.Reply{At: stamp("2026-10-22T13:22:15.956Z"), Source: "mbp", Tool: "codex", Session: "01a163c6-0ec2-7283-bdb0-53a0e17d7904", Repo: "Widget"}
	if sink.got[0] != want {
		t.Fatalf("first reply = %+v, want %+v", sink.got[0], want)
	}
	for _, raw := range sink.raw {
		if strings.Contains(raw, "PROMPT") || strings.Contains(raw, "scrubbed") {
			t.Fatalf("prompt text in a request: %s", raw)
		}
	}
	runOnce(context.Background(), w, nil, client)
	if len(sink.got) != 2 {
		t.Fatalf("an unchanged rollout posted again: %+v", sink.got)
	}

	sink.got = nil
	w2 := newTestWatcher(dir, now)
	w2.cfg.SendProject = true
	runOnce(context.Background(), w2, nil, client)
	if len(sink.got) != 2 || sink.got[0] != want {
		t.Fatalf("a restart replays %+v, want the same 2 for the server to ignore", sink.got)
	}
}

func TestCodexExecAndDesktopSessionsMakeNoReplies(t *testing.T) {
	sink := &codexReplySink{}
	srv := sink.server(t)
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-exec.jsonl", now, tuiFixture(t, "codex_exec", "exec")...)
	writeRollout(t, dir, "rollout-exec-cli.jsonl", now, tuiFixture(t, "codex_exec", "cli")...)
	writeRollout(t, dir, "rollout-desktop.jsonl", now, tuiFixture(t, "Codex Desktop", "vscode")...)
	w := newTestWatcher(dir, now)
	runOnce(context.Background(), w, nil, producer.NewClient(srv.URL, "", time.Second))
	if len(sink.raw) != 0 {
		t.Fatalf("replies posted for non-TUI sessions: %v", sink.raw)
	}
}

func TestCodexRepliesRetryAfterAFailedPost(t *testing.T) {
	sink := &codexReplySink{status: http.StatusNotFound}
	srv := sink.server(t)
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-tui.jsonl", now, tuiFixture(t, "codex-tui", "cli")...)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)
	runOnce(context.Background(), w, nil, client)
	if len(sink.raw) != 1 || len(w.replies) != 2 {
		t.Fatalf("posts = %d, pending = %d; want 1 failed post and 2 pending", len(sink.raw), len(w.replies))
	}
	runOnce(context.Background(), w, nil, client)
	if len(sink.raw) != 1 {
		t.Fatalf("retried before %v", replyRetryAfter)
	}
	sink.mu.Lock()
	sink.status = 0
	sink.mu.Unlock()
	w.now = func() time.Time { return now.Add(replyRetryAfter) }
	runOnce(context.Background(), w, nil, client)
	if len(sink.got) != 2 || len(w.replies) != 0 {
		t.Fatalf("retry posted %+v, pending %d", sink.got, len(w.replies))
	}
}
