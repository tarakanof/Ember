package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

type replyServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	raw    []string
	got    []producer.Reply
}

func newReplyServer(t *testing.T) *replyServer {
	t.Helper()
	s := &replyServer{status: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/v1/day/replies" {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.raw = append(s.raw, string(b))
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		var body struct {
			Replies []producer.Reply `json:"replies"`
		}
		_ = json.Unmarshal(b, &body)
		s.got = append(s.got, body.Replies...)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *replyServer) take() []producer.Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.got
	s.got = nil
	return out
}

func promptRow(id, thread, at, createdBy, text string, streaming int) string {
	payload, _ := json.Marshal(map[string]any{"createdBy": createdBy, "creationSource": "web", "text": text})
	return fmt.Sprintf(`INSERT INTO orchestration_v2_projection_messages (message_id, thread_id, role, streaming, created_at, updated_at, payload_json) VALUES (%q, %q, 'user', %d, %q, %q, '%s')`,
		id, thread, streaming, at, at, payload)
}

func threadRow(id, project, payload string) string {
	return fmt.Sprintf(`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, payload_json) VALUES (%q, %q, 't', 'claudeAgent', 'full-access', 'default', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', '%s')`,
		id, project, payload)
}

func newReplyDaemon(t *testing.T, home string, srv *replyServer, now time.Time) *daemon {
	t.Helper()
	cfg := Config{Common: producer.DefaultCommon(), T3Home: home, StateDir: t.TempDir(), ActivityWindowSeconds: 300}
	cfg.Source = "m4"
	d := newDaemon(cfg, producer.NewClient(srv.srv.URL, "tok", time.Second))
	d.alive = func(string) bool { return true }
	d.now = func() time.Time { return now }
	t.Cleanup(d.store.Close)
	t.Cleanup(d.prompts.Close)
	return d
}

func seedPrompts(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (67, 'x')`,
		`INSERT INTO projection_projects (project_id, title, workspace_root, scripts_json, created_at, updated_at) VALUES ('p-gizmo', 'Gizmo', '/nonexistent/Github/Gizmo', '[]', 'x', 'x')`,
		threadRow("th-wt", "p-gizmo", `{"worktreePath":"/nonexistent/.t3/worktrees/Widget/t3-1"}`),
		threadRow("th-root", "p-gizmo", `{}`),
		threadRow("th-sub", "p-gizmo", `{"lineage":{"relationshipToParent":"subagent"}}`),
		promptRow("m1", "th-wt", "2026-10-29T09:00:00.000Z", "user", "PROMPT fix the login", 0),
		promptRow("m2", "th-root", "2026-10-29T09:05:00.000Z", "user", "PROMPT and the tests", 0),
		promptRow("m3", "th-wt", "2026-10-29T09:06:00.000Z", "agent", "PROMPT delegated brief", 0),
		promptRow("m4", "th-wt", "2026-10-29T09:07:00.000Z", "user", "<task-notification>PROMPT done</task-notification>", 0),
		promptRow("m5", "th-wt", "2026-10-29T09:08:00.000Z", "user", "<task>\nPROMPT review only", 0),
		promptRow("m6", "th-sub", "2026-10-29T09:09:00.000Z", "user", "PROMPT subagent", 0),
		promptRow("m7", "th-wt", "2026-10-29T09:10:00.000Z", "user", "PROMPT still streaming", 1),
		promptRow("m8", "th-wt", "2026-08-01T09:00:00.000Z", "user", "PROMPT too old", 0),
	)
	return home
}

func TestT3PostsOnlyTheUsersOwnPromptsOnce(t *testing.T) {
	srv := newReplyServer(t)
	home := seedPrompts(t)
	now := time.Date(2026, 10, 29, 9, 10, 30, 0, time.UTC)
	d := newReplyDaemon(t, home, srv, now)
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	stamp := func(s string) int64 { tm, _ := time.Parse(time.RFC3339, s); return tm.Unix() }
	want := []producer.Reply{
		{At: stamp("2026-10-29T09:00:00Z"), Source: "m4", Tool: "t3", Session: "th-wt", Repo: "Widget"},
		{At: stamp("2026-10-29T09:05:00Z"), Source: "m4", Tool: "t3", Session: "th-root", Repo: "Gizmo"},
	}
	got := srv.take()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("replies = %+v, want %+v", got, want)
	}
	for _, raw := range srv.raw {
		if strings.Contains(raw, "PROMPT") {
			t.Fatalf("prompt text in a request: %s", raw)
		}
	}

	d.nextReplies = time.Time{}
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 0 {
		t.Fatalf("second poll posted %+v; the cursor must hold", got)
	}

	db, err := sql.Open("sqlite", filepath.Join(home, "userdata", "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(promptRow("m9", "th-root", "2026-10-29T09:10:10.000Z", "user", "PROMPT next", 0)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	d.nextReplies = time.Time{}
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 1 || got[0].Session != "th-root" || got[0].At != stamp("2026-10-29T09:10:10Z") {
		t.Fatalf("new prompt = %+v", got)
	}
}

func TestT3KeepsTheCursorWhenThePostFails(t *testing.T) {
	srv := newReplyServer(t)
	srv.status = http.StatusNotFound
	home := seedPrompts(t)
	now := time.Date(2026, 10, 29, 9, 10, 30, 0, time.UTC)
	d := newReplyDaemon(t, home, srv, now)
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.replyCursorPath()); !os.IsNotExist(err) {
		t.Fatalf("cursor saved after a failed post: %v", err)
	}
	if want := now.Add(replyRetryAfter); !d.nextReplies.Equal(want) {
		t.Fatalf("next try = %v, want %v", d.nextReplies, want)
	}
	srv.mu.Lock()
	srv.status = http.StatusOK
	srv.mu.Unlock()
	d.now = func() time.Time { return now.Add(replyRetryAfter) }
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 2 {
		t.Fatalf("retry posted %+v, want the 2 prompts", got)
	}
}

func TestT3PromptsWaitBetweenReads(t *testing.T) {
	srv := newReplyServer(t)
	home := seedPrompts(t)
	now := time.Date(2026, 10, 29, 9, 10, 30, 0, time.UTC)
	d := newReplyDaemon(t, home, srv, now)
	_ = d.poll(context.Background())
	srv.take()
	if err := os.Remove(d.replyCursorPath()); err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return now.Add(replyPollInterval - time.Second) }
	_ = d.poll(context.Background())
	if got := srv.take(); len(got) != 0 {
		t.Fatalf("read again after %v: %+v", replyPollInterval-time.Second, got)
	}
	d.now = func() time.Time { return now.Add(replyPollInterval) }
	_ = d.poll(context.Background())
	if got := srv.take(); len(got) != 2 {
		t.Fatalf("a lost cursor replays %d prompts, want 2 for the server to ignore", len(got))
	}
}
