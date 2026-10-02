package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeDB builds <home>/userdata/<file> from a testdata schema plus extra SQL.
func makeDB(t *testing.T, home, file, schema string, stmts ...string) {
	t.Helper()
	dir := filepath.Join(home, "userdata")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile(filepath.Join("testdata", schema))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range append([]string{string(ddl)}, stmts...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

func byID(threads []thread) map[string]thread {
	out := map[string]thread{}
	for _, th := range threads {
		out[th.ID] = th
	}
	return out
}

func TestReadSnapshotNoDatabase(t *testing.T) {
	_, err := readSnapshot(context.Background(), t.TempDir())
	if !errors.Is(err, errNoDatabase) {
		t.Fatalf("err = %v, want errNoDatabase", err)
	}
}

func TestReadSnapshotV1(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (53, 'PullRequestFilesViewed'), (54, 'ProjectionThreadsAutoSettleDisabledAt')`,
		`INSERT INTO projection_threads (thread_id, project_id, title, model, created_at, updated_at, pending_approval_count, pending_user_input_count, archived_at, deleted_at) VALUES
		 ('th-run', 'p', 'Fix login bug', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:05:00.000Z', 0, 0, NULL, NULL),
		 ('th-ask', 'p', 'Refactor', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:06:00.000Z', 1, 0, NULL, NULL),
		 ('th-input', 'p', 'Plan', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:06:00.000Z', 0, 2, NULL, NULL),
		 ('th-arch', 'p', 'Old', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:06:00.000Z', 0, 0, '2026-10-02T10:07:00.000Z', NULL),
		 ('th-del', 'p', 'Gone', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:06:00.000Z', 0, 0, NULL, '2026-10-02T10:07:00.000Z'),
		 ('th-nosess', 'p', 'Fresh', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', 0, 0, NULL, NULL)`,
		`INSERT INTO projection_thread_sessions (thread_id, status, last_error, updated_at) VALUES
		 ('th-run', 'running', NULL, '2026-10-02T10:05:30.000Z'),
		 ('th-ask', 'running', NULL, '2026-10-02T10:06:00.000Z'),
		 ('th-input', 'ready', NULL, '2026-10-02T10:06:00.000Z'),
		 ('th-arch', 'error', 'boom', '2026-10-02T10:06:00.000Z')`,
	)
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != 1 || snap.Migration != 54 {
		t.Fatalf("schema/migration = %d/%d, want 1/54", snap.Schema, snap.Migration)
	}
	got := byID(snap.Threads)
	if _, ok := got["th-del"]; ok {
		t.Fatal("deleted thread must not be read")
	}
	if len(got) != 5 {
		t.Fatalf("threads = %d, want 5: %+v", len(got), snap.Threads)
	}
	run := got["th-run"]
	if run.Title != "Fix login bug" || run.Status != "running" || run.PendingKind != "" {
		t.Fatalf("th-run = %+v", run)
	}
	if want := time.Date(2026, 10, 2, 10, 5, 30, 0, time.UTC); !run.ChangedAt.Equal(want) {
		t.Fatalf("th-run ChangedAt = %v, want %v (latest of thread/session)", run.ChangedAt, want)
	}
	if got["th-ask"].PendingKind != "approval" {
		t.Fatalf("th-ask pending = %q, want approval", got["th-ask"].PendingKind)
	}
	if got["th-input"].PendingKind != "user_input" {
		t.Fatalf("th-input pending = %q, want user_input", got["th-input"].PendingKind)
	}
	if a := got["th-arch"]; !a.Archived || a.LastError != "boom" {
		t.Fatalf("th-arch = %+v", a)
	}
	if got["th-nosess"].Status != "" {
		t.Fatalf("thread without session status = %q, want empty", got["th-nosess"].Status)
	}
}

func TestReadSnapshotV2(t *testing.T) {
	home := t.TempDir()
	// A leftover v1 file must be ignored once statev2.sqlite exists.
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (55, 'OrchestrationV2'), (56, 'RemoveRedundantProjectionIndexes')`,
		`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, archived_at, deleted_at, payload_json) VALUES
		 ('t-run', 'p', 'Add dark mode', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-wait', 'p', 'Ship it', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-done-ask', 'p', 'Question', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-fail', 'p', 'Broken', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-held', 'p', 'Queued behind', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-arch', 'p', 'Archived', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"archivedAt":"2026-10-02T11:00:00.000Z"}'),
		 ('t-idle', 'p', 'Empty', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}'),
		 ('t-del', 'p', 'Deleted', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, '2026-10-02T10:00:00.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, completed_at, payload_json) VALUES
		 ('r1', 't-run', 1, 'codex', 'completed', '2026-10-02T10:01:00.000Z', '2026-10-02T10:02:00.000Z', '{}'),
		 ('r2', 't-run', 2, 'codex', 'running', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r3', 't-wait', 1, 'claudeAgent', 'waiting', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r4', 't-done-ask', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r5', 't-fail', 1, 'codex', 'failed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r6', 't-held', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r7', 't-held', 2, 'codex', 'queued', '2026-10-02T10:05:00.000Z', NULL, '{"queueHeld":true}')`,
		`INSERT INTO orchestration_v2_projection_runtime_requests (runtime_request_id, thread_id, node_id, kind, status, created_at, payload_json) VALUES
		 ('q1', 't-done-ask', 'n', 'user_input', 'pending', '2026-10-02T10:04:30.000Z', '{}'),
		 ('q2', 't-run', 'n', 'command', 'resolved', '2026-10-02T10:03:30.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_provider_sessions (provider_session_id, thread_id, provider, status, updated_at, payload_json) VALUES
		 ('s-old', 't-fail', 'codex', 'error', '2026-10-02T10:00:00.000Z', '{"lastError":"stale"}'),
		 ('s-new', 't-fail', 'codex', 'error', '2026-10-02T10:04:00.000Z', '{"lastError":"rate limited"}')`,
	)
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != 2 || snap.Migration != 56 {
		t.Fatalf("schema/migration = %d/%d, want 2/56", snap.Schema, snap.Migration)
	}
	got := byID(snap.Threads)
	if _, ok := got["t-del"]; ok {
		t.Fatal("deleted thread must not be read")
	}
	cases := map[string]struct {
		status, pending, lastErr string
		archived                 bool
	}{
		"t-run":      {status: "running"},
		"t-wait":     {status: "waiting"},
		"t-done-ask": {status: "completed", pending: "user_input"},
		"t-fail":     {status: "failed", lastErr: "rate limited"},
		"t-held":     {status: "completed"},
		"t-arch":     {archived: true},
		"t-idle":     {},
	}
	for id, want := range cases {
		th, ok := got[id]
		if !ok {
			t.Fatalf("%s missing", id)
		}
		if th.Status != want.status || th.PendingKind != want.pending || th.LastError != want.lastErr || th.Archived != want.archived {
			t.Errorf("%s = %+v, want %+v", id, th, want)
		}
	}
	if want := time.Date(2026, 10, 2, 10, 4, 30, 0, time.UTC); !got["t-done-ask"].ChangedAt.Equal(want) {
		t.Fatalf("t-done-ask ChangedAt = %v, want %v (pending request time)", got["t-done-ask"].ChangedAt, want)
	}
	if got["t-run"].Title != "Add dark mode" {
		t.Fatalf("title = %q", got["t-run"].Title)
	}
}

func TestReadSnapshotUnknownSchemaFailsSoft(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v1.sql") // v2 file without v2 tables
	_, err := readSnapshot(context.Background(), home)
	if !errors.Is(err, errUnknownSchema) {
		t.Fatalf("err = %v, want errUnknownSchema", err)
	}
}

func TestReadSnapshotMissingColumnFailsSoft(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql",
		`ALTER TABLE projection_threads DROP COLUMN pending_user_input_count`)
	_, err := readSnapshot(context.Background(), home)
	if !errors.Is(err, errUnknownSchema) {
		t.Fatalf("err = %v, want errUnknownSchema", err)
	}
}

func TestReadSnapshotIsReadOnly(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	path := filepath.Join(home, "userdata", "state.sqlite")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ember_probe (x INTEGER)`); err == nil {
		t.Fatal("write on the T3 database succeeded; it must be opened read-only")
	}
	after, _ := os.Stat(path)
	if after.Size() != before.Size() {
		t.Fatal("database file changed")
	}
}
