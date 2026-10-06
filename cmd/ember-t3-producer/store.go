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
	errNoDatabase    = errors.New("no T3 Code database")
	errUnknownSchema = errors.New("unrecognised T3 Code schema")
)

type thread struct {
	ID              string
	Title           string
	Schema          int
	Status          string
	Active          string
	HoldsCompletion bool
	PendingKind     string
	LastError       string
	Archived        bool
	// updated_at is deliberately unused: settling, renaming and archiving bump it.
	ChangedAt time.Time
}

type snapshot struct {
	Schema    int
	Migration int
	Threads   []thread
}

func readSnapshot(ctx context.Context, home string) (snapshot, error) {
	r := &storeReader{}
	defer r.Close()
	return r.Read(ctx, home)
}

type storeReader struct {
	path string
	info os.FileInfo
	db   *sql.DB
}

func (r *storeReader) Read(ctx context.Context, home string) (snapshot, error) {
	dir := filepath.Join(home, "userdata")
	v1, v2 := filepath.Join(dir, "state.sqlite"), filepath.Join(dir, "statev2.sqlite")
	m1, ok1 := dbModTime(v1)
	m2, ok2 := dbModTime(v2)
	switch {
	case ok2 && (!ok1 || !m1.After(m2)):
		return r.readWith(ctx, v2, 2, v2Tables, queryV2)
	case ok1:
		return r.readWith(ctx, v1, 1, v1Tables, queryV1)
	}
	r.Close()
	return snapshot{}, errNoDatabase
}

func (r *storeReader) Close() {
	if r.db != nil {
		r.db.Close()
	}
	r.db, r.path, r.info = nil, "", nil
}

func (r *storeReader) handle(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		r.Close()
		return nil, err
	}
	if r.db != nil && (r.path != path || !os.SameFile(r.info, info)) {
		r.Close()
	}
	if r.db == nil {
		db, err := openReadOnly(path)
		if err != nil {
			return nil, err
		}
		r.db, r.path, r.info = db, path, info
	}
	return r.db, nil
}

func dbModTime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return time.Time{}, false
	}
	m := info.ModTime()
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.ModTime().After(m) {
		m = wal.ModTime()
	}
	return m, true
}

var (
	v1Tables = []string{"projection_threads", "projection_thread_sessions"}
	v2Tables = []string{
		"orchestration_v2_projection_threads", "orchestration_v2_projection_runs",
		"orchestration_v2_projection_runtime_requests", "orchestration_v2_projection_provider_sessions",
		"orchestration_v2_projection_provider_session_bindings", "orchestration_v2_projection_turn_items",
		"orchestration_v2_projection_provider_threads",
	}
)

func (r *storeReader) readWith(ctx context.Context, path string, schema int, tables []string, query string) (snapshot, error) {
	db, err := r.handle(path)
	if err != nil {
		return snapshot{}, err
	}
	snap, err := readTables(ctx, db, path, schema, tables, query)
	if err != nil && !errors.Is(err, errUnknownSchema) {
		r.Close()
	}
	return snap, err
}

func readTables(ctx context.Context, db *sql.DB, path string, schema int, tables []string, query string) (snapshot, error) {
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
			stamps                             [3]sql.NullString
			active                             sql.NullString
			holds                              sql.NullInt64
		)
		if err := rows.Scan(&id, &title, &status, &pending, &lastErr, &archived, &stamps[0], &stamps[1], &stamps[2], &active, &holds); err != nil {
			return snapshot{}, fmt.Errorf("scan %s: %w", filepath.Base(path), err)
		}
		th := thread{
			ID: id, Title: title, Schema: schema,
			Status: status.String, PendingKind: pending.String, LastError: lastErr.String,
			Archived: archived.Valid && archived.String != "",
			Active:   active.String, HoldsCompletion: holds.Int64 != 0,
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

// mode=ro still reads the live WAL (immutable=1 would not).
func openReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

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
  s.updated_at,
  NULL,
  NULL,
  NULL,
  0
FROM projection_threads t
LEFT JOIN projection_thread_sessions s ON s.thread_id = t.thread_id
WHERE t.deleted_at IS NULL`

const queryV2 = `
SELECT
  t.thread_id,
  t.title,
  presented.status,
  (SELECT q.kind FROM orchestration_v2_projection_runtime_requests q
    WHERE q.thread_id = t.thread_id AND q.status = 'pending' AND q.kind <> 'auth_refresh'
    ORDER BY q.created_at DESC, q.runtime_request_id DESC LIMIT 1),
  COALESCE(
    (SELECT json_extract(ps.payload_json, '$.lastError') FROM orchestration_v2_projection_provider_sessions ps
      INNER JOIN orchestration_v2_projection_provider_session_bindings b
        ON b.provider_session_id = ps.provider_session_id
      WHERE b.thread_id = t.thread_id AND ps.provider_instance_id = t.provider_instance_id
      ORDER BY ps.updated_at DESC, ps.provider_session_id DESC LIMIT 1),
    (SELECT json_extract(i.payload_json, '$.failure.message') FROM orchestration_v2_projection_turn_items i
      WHERE presented.status = 'failed' AND i.run_id = presented.run_id AND i.thread_id = t.thread_id
        AND i.type = 'error' AND i.status = 'failed'
        AND i.node_id IS json_extract(presented.payload_json, '$.rootNodeId')
      ORDER BY i.updated_at DESC, i.ordinal DESC, i.turn_item_id DESC LIMIT 1)),
  COALESCE(t.archived_at, json_extract(t.payload_json, '$.archivedAt')),
  presented.requested_at,
  presented.completed_at,
  (SELECT max(q.created_at) FROM orchestration_v2_projection_runtime_requests q
    WHERE q.thread_id = t.thread_id AND q.status = 'pending' AND q.kind <> 'auth_refresh'),
  (SELECT a.status FROM orchestration_v2_projection_runs a
    WHERE a.thread_id = t.thread_id AND a.status IN ('preparing', 'starting', 'running', 'waiting')
    ORDER BY a.ordinal DESC, a.run_id DESC LIMIT 1),
  CASE WHEN presented.status = 'completed' AND (
    EXISTS (SELECT 1 FROM orchestration_v2_projection_provider_threads pt,
        json_each(pt.payload_json, '$.pendingBackgroundTasks') task
      -- The payload's activeProviderThreadId, as T3 decodes it (the
      -- active_provider_thread_id column is not what T3's shell reads).
      WHERE pt.thread_id = t.thread_id
        AND (json_extract(t.payload_json, '$.activeProviderThreadId') IS NULL
          OR pt.provider_thread_id = json_extract(t.payload_json, '$.activeProviderThreadId'))
        AND length(COALESCE(json_extract(task.value, '$.taskId'), '')) > 0
        AND json_extract(task.value, '$.kind') IS NOT 'command')
    OR EXISTS (SELECT 1 FROM orchestration_v2_projection_turn_items i
      LEFT JOIN orchestration_v2_projection_runs ir ON ir.run_id = i.run_id
      -- T3's turn_items_recovery_idx is partial on exactly this type list and
      -- status list; SQLite only uses it when the WHERE repeats them verbatim,
      -- else it scans every item of every completed thread (~0.5 s per poll
      -- on 600k items). command_execution never holds, so exclude it after.
      WHERE i.thread_id = t.thread_id
        AND i.type IN ('command_execution', 'dynamic_tool', 'subagent') AND i.type <> 'command_execution'
        AND i.status IN ('pending', 'running', 'waiting')
        AND NOT (i.type = 'dynamic_tool' AND json_type(i.payload_json, '$.input.persistent') = 'true')
        AND (i.run_id IS NULL OR ir.status <> 'rolled_back'))
  ) THEN 1 ELSE 0 END
FROM orchestration_v2_projection_threads t
LEFT JOIN orchestration_v2_projection_runs presented ON presented.run_id = (
  SELECT c.run_id FROM orchestration_v2_projection_runs c
  WHERE c.thread_id = t.thread_id
    AND NOT (c.status = 'queued' AND json_extract(c.payload_json, '$.queueHeld') IS 1)
  ORDER BY c.ordinal DESC, c.run_id DESC LIMIT 1)
WHERE t.deleted_at IS NULL
  AND json_extract(t.payload_json, '$.lineage.relationshipToParent') IS NOT 'subagent'`

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
