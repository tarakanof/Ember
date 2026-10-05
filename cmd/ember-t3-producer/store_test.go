package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		 ('th-run', 'p', 'Fix login bug', 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-05T09:00:00.000Z', 0, 0, NULL, NULL),
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
		t.Fatalf("th-run ChangedAt = %v, want %v (session time, not the settle-bumped thread time)", run.ChangedAt, want)
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
	// A leftover v1 file must be ignored while statev2.sqlite is the newer one.
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (55, 'OrchestrationV2'), (56, 'RemoveRedundantProjectionIndexes')`,
		// updated_at is deliberately late (settle, rename, archive bump it): it must not drive ChangedAt.
		`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, archived_at, deleted_at, payload_json, provider_instance_id) VALUES
		 ('t-run', 'p', 'Add dark mode', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-05T09:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-wait', 'p', 'Ship it', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-done-ask', 'p', 'Question', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-fail', 'p', 'Broken', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-fail-sess', 'p', 'Broken too', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-held', 'p', 'Queued behind', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-arch', 'p', 'Archived', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"archivedAt":"2026-10-02T11:00:00.000Z"}', 'codex'),
		 ('t-idle', 'p', 'Empty', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-auth', 'p', 'Needs approval', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-settled', 'p', 'Settled days later', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-05T09:00:00.000Z', NULL, NULL, '{"settledAt":"2026-10-05T09:00:00.000Z"}', 'codex'),
		 ('t-sub', 'p', 'Subagent: explore', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"lineage":{"relationshipToParent":"subagent"}}', 'codex'),
		 ('t-fork', 'p', 'Forked', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"lineage":{"relationshipToParent":"fork"}}', 'codex'),
		 ('t-q', 'p', 'Queued behind active', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-bg-roster', 'p', 'Roster monitor', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"activeProviderThreadId":"pt-a"}', 'claudeAgent'),
		 ('t-bg-other', 'p', 'Roster of inactive provider thread', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{"activeProviderThreadId":"pt-b"}', 'claudeAgent'),
		 ('t-bg-cmd', 'p', 'Dev server only', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-bg-sub', 'p', 'Subagent item', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-bg-persist', 'p', 'Persistent monitor', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-bg-rolled', 'p', 'Rolled back subagent', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'codex'),
		 ('t-bg-nokind', 'p', 'Roster no kind', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-bg-unk', 'p', 'Roster unknown kind', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-bg-noid', 'p', 'Roster empty id', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-bg-failed', 'p', 'Failed with open work', 'claudeAgent', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, NULL, '{}', 'claudeAgent'),
		 ('t-del', 'p', 'Deleted', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', NULL, '2026-10-02T10:00:00.000Z', '{}', 'codex')`,
		`INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, completed_at, payload_json) VALUES
		 ('r1', 't-run', 1, 'codex', 'completed', '2026-10-02T10:01:00.000Z', '2026-10-02T10:02:00.000Z', '{}'),
		 ('r2', 't-run', 2, 'codex', 'running', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r3', 't-wait', 1, 'claudeAgent', 'waiting', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r4', 't-done-ask', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r5', 't-fail', 1, 'codex', 'failed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{"rootNodeId":"root-5"}'),
		 ('r5b', 't-fail-sess', 1, 'codex', 'failed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{"rootNodeId":"root-5b"}'),
		 ('r6', 't-held', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r7', 't-held', 2, 'codex', 'queued', '2026-10-02T10:05:00.000Z', NULL, '{"queueHeld":true}'),
		 ('r8', 't-auth', 1, 'codex', 'running', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r9', 't-settled', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r10', 't-sub', 1, 'codex', 'running', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r11', 't-q', 1, 'codex', 'running', '2026-10-02T10:03:00.000Z', NULL, '{}'),
		 ('r12', 't-q', 2, 'codex', 'queued', '2026-10-02T10:04:00.000Z', NULL, '{}'),
		 ('r13', 't-bg-roster', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r14', 't-bg-other', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r15', 't-bg-cmd', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r16', 't-bg-sub', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r17', 't-bg-persist', 1, 'codex', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r18', 't-bg-rolled', 1, 'codex', 'rolled_back', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r19', 't-bg-rolled', 2, 'codex', 'completed', '2026-10-02T10:05:00.000Z', '2026-10-02T10:06:00.000Z', '{}'),
		 ('r20', 't-bg-nokind', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r21', 't-bg-unk', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r22', 't-bg-noid', 1, 'claudeAgent', 'completed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}'),
		 ('r23', 't-bg-failed', 1, 'claudeAgent', 'failed', '2026-10-02T10:03:00.000Z', '2026-10-02T10:04:00.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_provider_threads (provider_thread_id, thread_id, provider, status, updated_at, payload_json) VALUES
		 ('pt-a', 't-bg-roster', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"m1","kind":"monitor"}]}'),
		 ('pt-b', 't-bg-other', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[]}'),
		 ('pt-old', 't-bg-other', 'claudeAgent', 'stale', '2026-10-02T10:03:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"old","kind":"subagent"}]}'),
		 ('pt-d', 't-bg-nokind', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"x"}]}'),
		 ('pt-e', 't-bg-unk', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"y","kind":"weird"}]}'),
		 ('pt-f', 't-bg-noid', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"","kind":"monitor"}]}'),
		 ('pt-g', 't-bg-failed', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"z","kind":"monitor"}]}'),
		 ('pt-c', 't-bg-cmd', 'claudeAgent', 'active', '2026-10-02T10:04:00.000Z', '{"pendingBackgroundTasks":[{"taskId":"dev","kind":"command"}]}')`,
		`INSERT INTO orchestration_v2_projection_turn_items (turn_item_id, thread_id, run_id, ordinal, type, status, updated_at, payload_json) VALUES
		 ('b-sub', 't-bg-sub', 'r16', 1, 'subagent', 'running', '2026-10-02T10:03:30.000Z', '{}'),
		 ('b-cmd', 't-bg-cmd', 'r15', 1, 'command_execution', 'running', '2026-10-02T10:03:30.000Z', '{}'),
		 ('b-persist', 't-bg-persist', 'r17', 1, 'dynamic_tool', 'running', '2026-10-02T10:03:30.000Z', '{"input":{"persistent":true}}'),
		 ('b-failed', 't-bg-failed', 'r23', 1, 'subagent', 'running', '2026-10-02T10:03:30.000Z', '{}'),
		 ('b-rolled', 't-bg-rolled', 'r18', 1, 'subagent', 'running', '2026-10-02T10:03:30.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_runtime_requests (runtime_request_id, thread_id, node_id, kind, status, created_at, payload_json) VALUES
		 ('q1', 't-done-ask', 'n', 'user_input', 'pending', '2026-10-02T10:04:30.000Z', '{}'),
		 ('q2', 't-run', 'n', 'command', 'resolved', '2026-10-02T10:03:30.000Z', '{}'),
		 ('q3', 't-auth', 'n', 'command', 'pending', '2026-10-02T10:03:10.000Z', '{}'),
		 ('q4', 't-auth', 'n', 'auth_refresh', 'pending', '2026-10-02T10:03:20.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_turn_items (turn_item_id, thread_id, run_id, node_id, ordinal, type, status, updated_at, payload_json) VALUES
		 ('i-child', 't-fail', 'r5', 'child', 1, 'error', 'failed', '2026-10-02T10:03:59.000Z', '{"failure":{"message":"subagent failed"}}'),
		 ('i-root', 't-fail', 'r5', 'root-5', 2, 'error', 'failed', '2026-10-02T10:03:58.000Z', '{"failure":{"message":"usage limit reached"}}'),
		 -- T3 shows sessionError ?? failure.message: the session's text must win here.
		 ('i-root-b', 't-fail-sess', 'r5b', 'root-5b', 1, 'error', 'failed', '2026-10-02T10:03:58.000Z', '{"failure":{"message":"item loses"}}')`,
		`INSERT INTO orchestration_v2_projection_provider_sessions (provider_session_id, thread_id, provider, status, updated_at, payload_json, provider_instance_id) VALUES
		 ('s-fail', 't-fail', 'codex', 'error', '2026-10-02T10:04:00.000Z', '{}', 'codex'),
		 ('s-old', 't-fail-sess', 'codex', 'error', '2026-10-02T10:00:00.000Z', '{"lastError":"stale"}', 'codex'),
		 ('s-new', 't-fail-sess', 'codex', 'error', '2026-10-02T10:04:00.000Z', '{"lastError":"rate limited"}', 'codex'),
		 ('s-other-instance', 't-fail-sess', 'claudeAgent', 'error', '2026-10-02T10:05:00.000Z', '{"lastError":"other provider"}', 'claudeAgent'),
		 ('s-unbound', 't-fail-sess', 'codex', 'error', '2026-10-02T10:06:00.000Z', '{"lastError":"not bound"}', 'codex')`,
		`INSERT INTO orchestration_v2_projection_provider_session_bindings (provider_session_id, thread_id) VALUES
		 ('s-fail', 't-fail'), ('s-old', 't-fail-sess'), ('s-new', 't-fail-sess'), ('s-other-instance', 't-fail-sess')`,
	)
	setMtime(t, filepath.Join(home, "userdata", "state.sqlite"), time.Now().Add(-time.Hour))
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != 2 || snap.Migration != 56 {
		t.Fatalf("schema/migration = %d/%d, want 2/56", snap.Schema, snap.Migration)
	}
	got := byID(snap.Threads)
	for _, id := range []string{"t-del", "t-sub"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s must not be read", id)
		}
	}
	at := func(hms string) time.Time {
		v, err := time.Parse(time.RFC3339, "2026-10-02T"+hms+"Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := map[string]struct {
		status, pending, lastErr, active string
		holds, archived                  bool
		changed                          time.Time
	}{
		"t-run":       {status: "running", active: "running", changed: at("10:03:00")},
		"t-wait":      {status: "waiting", active: "waiting", changed: at("10:03:00")},
		"t-done-ask":  {status: "completed", pending: "user_input", changed: at("10:04:30")},
		"t-fail":      {status: "failed", lastErr: "usage limit reached", changed: at("10:04:00")},
		"t-fail-sess": {status: "failed", lastErr: "rate limited", changed: at("10:04:00")},
		"t-held":      {status: "completed", changed: at("10:04:00")},
		"t-arch":      {archived: true},
		"t-idle":      {},
		"t-auth":      {status: "running", active: "running", pending: "command", changed: at("10:03:10")},
		"t-settled":   {status: "completed", changed: at("10:04:00")},
		"t-fork":      {},
		// A queued run behind an active one: the active run is the activity.
		"t-q":          {status: "queued", active: "running", changed: at("10:04:00")},
		"t-bg-roster":  {status: "completed", holds: true, changed: at("10:04:00")},
		"t-bg-other":   {status: "completed", changed: at("10:04:00")},
		"t-bg-cmd":     {status: "completed", changed: at("10:04:00")},
		"t-bg-sub":     {status: "completed", holds: true, changed: at("10:04:00")},
		"t-bg-persist": {status: "completed", changed: at("10:04:00")},
		"t-bg-nokind":  {status: "completed", holds: true, changed: at("10:04:00")},
		"t-bg-unk":     {status: "completed", holds: true, changed: at("10:04:00")},
		"t-bg-noid":    {status: "completed", changed: at("10:04:00")},
		"t-bg-failed":  {status: "failed", changed: at("10:04:00")},
		"t-bg-rolled":  {status: "completed", changed: at("10:06:00")},
	}
	for id, want := range cases {
		th, ok := got[id]
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if th.Status != want.status || th.PendingKind != want.pending || th.LastError != want.lastErr || th.Archived != want.archived || th.Active != want.active || th.HoldsCompletion != want.holds || !th.ChangedAt.Equal(want.changed) {
			t.Errorf("%s = %+v, want %+v", id, th, want)
		}
	}
	if got["t-run"].Title != "Add dark mode" {
		t.Fatalf("title = %q", got["t-run"].Title)
	}
}

func setMtime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestReadSnapshotPrefersNewerDatabase(t *testing.T) {
	// Downgrade from the 0.0.46 preview to 0.0.45: statev2.sqlite is left
	// behind and only state.sqlite is still written.
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql")
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	setMtime(t, filepath.Join(home, "userdata", "statev2.sqlite"), time.Now().Add(-48*time.Hour))
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != 1 {
		t.Fatalf("schema = %d, want 1 (state.sqlite is newer)", snap.Schema)
	}
	// A fresh WAL counts as a write even when the main file is old.
	wal := filepath.Join(home, "userdata", "statev2.sqlite-wal")
	if err := os.WriteFile(wal, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setMtime(t, filepath.Join(home, "userdata", "state.sqlite"), time.Now().Add(-time.Hour))
	if snap, err = readSnapshot(context.Background(), home); err != nil || snap.Schema != 2 {
		t.Fatalf("schema = %d err = %v, want 2 (statev2 WAL is newest)", snap.Schema, err)
	}
}

func TestReadSnapshotWALWithConcurrentWriter(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	w, err := sql.Open("sqlite", filepath.Join(home, "userdata", "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	for _, s := range []string{
		`PRAGMA journal_mode=WAL`,
		`INSERT INTO projection_threads (thread_id, project_id, title, model, created_at, updated_at) VALUES ('committed', 'p', 'Seen', 'm', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z')`,
		`BEGIN IMMEDIATE`,
		`INSERT INTO projection_threads (thread_id, project_id, title, model, created_at, updated_at) VALUES ('uncommitted', 'p', 'Hidden', 'm', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z')`,
	} {
		if _, err := w.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatalf("read while a writer holds the lock: %v", err)
	}
	got := byID(snap.Threads)
	if _, ok := got["committed"]; !ok {
		t.Fatal("committed WAL row not read")
	}
	if _, ok := got["uncommitted"]; ok {
		t.Fatal("uncommitted row read")
	}
	if _, err := w.Exec(`COMMIT`); err != nil {
		t.Fatalf("writer blocked by the reader: %v", err)
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

// The turn-item EXISTS must use T3's partial recovery index, not scan every
// item of every completed thread (~0.5 s per poll at 600k items).
func TestQueryV2UsesRecoveryIndex(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql")
	db, err := openReadOnly(filepath.Join(home, "userdata", "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("EXPLAIN QUERY PLAN " + queryV2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if !strings.Contains(plan.String(), "orchestration_v2_projection_turn_items_recovery_idx") {
		t.Fatalf("turn-item lookup no longer uses the recovery index:\n%s", plan.String())
	}
}

func tidFirst(t *testing.T, snap snapshot) string {
	t.Helper()
	if len(snap.Threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(snap.Threads))
	}
	return snap.Threads[0].Title
}

func insertV1(t *testing.T, path, id, title string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO projection_threads (thread_id, project_id, title, model, created_at, updated_at, pending_approval_count, pending_user_input_count, archived_at, deleted_at)
	 VALUES (?, 'p', ?, 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', 0, 0, NULL, NULL)`, id, title); err != nil {
		t.Fatal(err)
	}
}

func TestStoreReaderReusesHandleAndSeesNewRows(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (54, 'x')`)
	path := filepath.Join(home, "userdata", "state.sqlite")
	insertV1(t, path, "a", "first")
	r := &storeReader{}
	defer r.Close()
	snap, err := r.Read(context.Background(), home)
	if err != nil || tidFirst(t, snap) != "first" {
		t.Fatalf("first read: %v %v", snap, err)
	}
	db1 := r.db
	insertV1(t, path, "b", "second")
	snap, err = r.Read(context.Background(), home)
	if err != nil || len(snap.Threads) != 2 {
		t.Fatalf("second read: %v %v", snap, err)
	}
	if r.db != db1 {
		t.Fatal("handle must be reused across reads of the same file")
	}
}

func TestStoreReaderReopensWhenFileReplaced(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	path := filepath.Join(home, "userdata", "state.sqlite")
	insertV1(t, path, "a", "old")
	r := &storeReader{}
	defer r.Close()
	if snap, err := r.Read(context.Background(), home); err != nil || tidFirst(t, snap) != "old" {
		t.Fatalf("read old: %v %v", snap, err)
	}
	db1 := r.db
	if err := os.Rename(path, path+".bak"); err != nil {
		t.Fatal(err)
	}
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	insertV1(t, path, "z", "replacement")
	snap, err := r.Read(context.Background(), home)
	if err != nil || tidFirst(t, snap) != "replacement" {
		t.Fatalf("read after replace: %v %v", snap, err)
	}
	if r.db == db1 {
		t.Fatal("handle must be reopened when the file is replaced")
	}
}

func TestStoreReaderReopensAfterFailureAndWhenFileVanishes(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	path := filepath.Join(home, "userdata", "state.sqlite")
	insertV1(t, path, "a", "t")
	r := &storeReader{}
	defer r.Close()
	if _, err := r.Read(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	// A read failure (here: a cancelled context) drops the handle.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx, home); err == nil {
		t.Fatal("cancelled read must fail")
	}
	if r.db != nil {
		t.Fatal("handle must be dropped after a failed read")
	}
	if snap, err := r.Read(context.Background(), home); err != nil || tidFirst(t, snap) != "t" {
		t.Fatalf("read after failure: %v %v", snap, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), home); !errors.Is(err, errNoDatabase) {
		t.Fatalf("err = %v, want errNoDatabase", err)
	}
	if r.db != nil {
		t.Fatal("handle must be closed when the database is gone")
	}
}

// T3 runs its database in WAL mode with a writer that stays open; a reused
// read-only handle must keep seeing commits, checkpoints and a VACUUM.
func TestStoreReaderFollowsAHeldWALWriter(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	path := filepath.Join(home, "userdata", "state.sqlite")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	add := func(id string) {
		t.Helper()
		if _, err := w.Exec(`INSERT INTO projection_threads (thread_id, project_id, title, model, created_at, updated_at, pending_approval_count, pending_user_input_count, archived_at, deleted_at)
		 VALUES (?, 'p', ?, 'gpt-5', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', 0, 0, NULL, NULL)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	r := &storeReader{}
	defer r.Close()
	want := 0
	check := func(step string) {
		t.Helper()
		snap, err := r.Read(context.Background(), home)
		if err != nil || len(snap.Threads) != want {
			t.Fatalf("%s: threads=%d err=%v, want %d", step, len(snap.Threads), err, want)
		}
	}
	add("a")
	want++
	check("first commit")
	db1 := r.db
	add("b")
	want++
	check("commit in WAL")
	var busy, logFrames, ckpt int
	if err := w.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &ckpt); err != nil || busy != 0 {
		t.Fatalf("checkpoint blocked by the idle reader: busy=%d err=%v", busy, err)
	}
	check("after checkpoint")
	add("c")
	want++
	check("commit after checkpoint")
	if _, err := w.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	check("after VACUUM")
	add("d")
	want++
	check("commit after VACUUM")
	if r.db != db1 {
		t.Fatal("the handle should have survived commits, checkpoint and VACUUM")
	}
}

func TestDaemonPollClosesHandleWhileT3IsDown(t *testing.T) {
	home := t.TempDir()
	makeDB(t, home, "state.sqlite", "schema_v1.sql")
	d := newDaemon(Config{T3Home: home}, nil)
	up := true
	d.alive = func(string) bool { return up }
	if _, err := d.store.Read(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	if d.store.db == nil {
		t.Fatal("expected an open handle while T3 is up")
	}
	up = false
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.store.db != nil {
		t.Fatal("handle must be closed while T3 is down")
	}
}
