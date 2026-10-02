package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// errNoDatabase means T3 Code has never run under this home (not installed, or another T3CODE_HOME).
	errNoDatabase = errors.New("no T3 Code database")
	// errUnknownSchema means the database exists but lacks the tables or columns this producer reads.
	errUnknownSchema = errors.New("unrecognised T3 Code schema")
)

// thread is one T3 thread, normalised across the v1 and v2 orchestration schemas.
type thread struct {
	ID     string
	Title  string
	Schema int // 1 = projection_* (T3 <= 0.0.45), 2 = orchestration_v2_* (T3 >= 0.0.46)
	// Status is the v1 session status or the v2 latest-run status; "" when the thread never ran.
	Status string
	// PendingKind is the kind of the newest open request blocking on the user ("" = none).
	PendingKind string
	LastError   string
	Archived    bool
	// ChangedAt is the newest timestamp among the thread, its session/run and its pending request.
	ChangedAt time.Time
}

type snapshot struct {
	Schema    int
	Migration int // highest effect_sql_migrations id; 0 when the table is unreadable
	Threads   []thread
}

// readSnapshot reads every non-deleted thread from the T3 database under home.
// statev2.sqlite (T3 >= 0.0.46) wins over state.sqlite (T3 <= 0.0.45): the
// v2 server imports and then ignores the v1 file, which stays on disk.
func readSnapshot(ctx context.Context, home string) (snapshot, error) {
	dir := filepath.Join(home, "userdata")
	if p := filepath.Join(dir, "statev2.sqlite"); fileExists(p) {
		return readWith(ctx, p, 2, v2Tables, queryV2)
	}
	if p := filepath.Join(dir, "state.sqlite"); fileExists(p) {
		return readWith(ctx, p, 1, v1Tables, queryV1)
	}
	return snapshot{}, errNoDatabase
}

var (
	v1Tables = []string{"projection_threads", "projection_thread_sessions"}
	v2Tables = []string{"orchestration_v2_projection_threads", "orchestration_v2_projection_runs", "orchestration_v2_projection_runtime_requests", "orchestration_v2_projection_provider_sessions"}
)

func readWith(ctx context.Context, path string, schema int, tables []string, query string) (snapshot, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return snapshot{}, err
	}
	defer db.Close()
	for _, tbl := range tables {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, tbl).Scan(&n); err != nil {
			return snapshot{}, fmt.Errorf("read %s: %w", filepath.Base(path), err)
		}
		if n == 0 {
			return snapshot{}, fmt.Errorf("%w: %s has no %s", errUnknownSchema, filepath.Base(path), tbl)
		}
	}
	snap := snapshot{Schema: schema}
	var mig sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(migration_id) FROM effect_sql_migrations`).Scan(&mig); err == nil {
		snap.Migration = int(mig.Int64)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		if strings.Contains(err.Error(), "no such column") || strings.Contains(err.Error(), "no such table") {
			return snapshot{}, fmt.Errorf("%w: %v", errUnknownSchema, err)
		}
		return snapshot{}, fmt.Errorf("query %s: %w", filepath.Base(path), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, title                          string
			status, pending, lastErr, archived sql.NullString
			stamps                             [4]sql.NullString
		)
		if err := rows.Scan(&id, &title, &status, &pending, &lastErr, &archived, &stamps[0], &stamps[1], &stamps[2], &stamps[3]); err != nil {
			return snapshot{}, fmt.Errorf("scan %s: %w", filepath.Base(path), err)
		}
		th := thread{
			ID: id, Title: title, Schema: schema,
			Status: status.String, PendingKind: pending.String, LastError: lastErr.String,
			Archived: archived.Valid && archived.String != "",
		}
		for _, s := range stamps {
			if t, ok := parseStamp(s.String); ok && t.After(th.ChangedAt) {
				th.ChangedAt = t
			}
		}
		snap.Threads = append(snap.Threads, th)
	}
	return snap, rows.Err()
}

// openReadOnly opens a T3 database without ever writing to it. mode=ro still
// reads the live WAL (immutable=1 would not), and query_only refuses writes
// even if the driver ignored the mode.
func openReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// queryV1 mirrors the T3 0.0.45 shell: session status from projection_thread_sessions,
// hasPendingApprovals / hasPendingUserInput from the projection_threads counters.
const queryV1 = `
SELECT
  t.thread_id,
  t.title,
  s.status,
  CASE
    WHEN t.pending_approval_count > 0 THEN 'approval'
    WHEN t.pending_user_input_count > 0 THEN 'user_input'
  END,
  s.last_error,
  t.archived_at,
  t.updated_at,
  s.updated_at,
  NULL,
  NULL
FROM projection_threads t
LEFT JOIN projection_thread_sessions s ON s.thread_id = t.thread_id
WHERE t.deleted_at IS NULL`

// queryV2 is a reduced copy of ProjectionStore.selectShellThreadRows in T3
// 0.0.46: the presented run is the newest one not held in the queue, and a
// pending runtime request counts even after its run settled (Codex
// user_input requests outlive the turn).
const queryV2 = `
SELECT
  t.thread_id,
  t.title,
  presented.status,
  (SELECT q.kind FROM orchestration_v2_projection_runtime_requests q
    WHERE q.thread_id = t.thread_id AND q.status = 'pending'
    ORDER BY q.created_at DESC, q.runtime_request_id DESC LIMIT 1),
  (SELECT json_extract(ps.payload_json, '$.lastError') FROM orchestration_v2_projection_provider_sessions ps
    WHERE ps.thread_id = t.thread_id
    ORDER BY ps.updated_at DESC, ps.provider_session_id DESC LIMIT 1),
  COALESCE(t.archived_at, json_extract(t.payload_json, '$.archivedAt')),
  t.updated_at,
  presented.requested_at,
  presented.completed_at,
  (SELECT max(q.created_at) FROM orchestration_v2_projection_runtime_requests q
    WHERE q.thread_id = t.thread_id AND q.status = 'pending')
FROM orchestration_v2_projection_threads t
LEFT JOIN orchestration_v2_projection_runs presented ON presented.run_id = (
  SELECT c.run_id FROM orchestration_v2_projection_runs c
  WHERE c.thread_id = t.thread_id
    AND NOT (c.status = 'queued' AND json_extract(c.payload_json, '$.queueHeld') IS 1)
  ORDER BY c.ordinal DESC, c.run_id DESC LIMIT 1)
WHERE t.deleted_at IS NULL`

func parseStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
