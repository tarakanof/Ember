-- Subset of T3 Code v0.0.45 <T3CODE_HOME>/userdata/state.sqlite, transcribed
-- from apps/server/src/persistence/Migrations/{005,006,010,012,017,023,028}.
-- Only the tables and columns ember-t3-producer reads, plus a few neighbours
-- so the queries are exercised against a realistic shape.
CREATE TABLE effect_sql_migrations (
  migration_id INTEGER PRIMARY KEY NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  name VARCHAR(255) NOT NULL
);
CREATE TABLE projection_threads (
  thread_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  title TEXT NOT NULL,
  model TEXT NOT NULL,
  branch TEXT,
  worktree_path TEXT,
  latest_turn_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  runtime_mode TEXT NOT NULL DEFAULT 'full-access',
  interaction_mode TEXT NOT NULL DEFAULT 'default',
  archived_at TEXT,
  latest_user_message_at TEXT,
  pending_approval_count INTEGER NOT NULL DEFAULT 0,
  pending_user_input_count INTEGER NOT NULL DEFAULT 0,
  has_actionable_proposed_plan INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE projection_thread_sessions (
  thread_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  provider_name TEXT,
  provider_session_id TEXT,
  provider_thread_id TEXT,
  active_turn_id TEXT,
  last_error TEXT,
  updated_at TEXT NOT NULL,
  runtime_mode TEXT NOT NULL DEFAULT 'full-access',
  provider_instance_id TEXT
);
