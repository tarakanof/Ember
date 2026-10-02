-- Subset of T3 Code v0.0.46-preview <T3CODE_HOME>/userdata/statev2.sqlite,
-- transcribed from apps/server/src/persistence/Migrations/055_OrchestrationV2.ts
-- and OrchestrationV2/Foundation.ts. Only what ember-t3-producer reads.
CREATE TABLE effect_sql_migrations (
  migration_id INTEGER PRIMARY KEY NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  name VARCHAR(255) NOT NULL
);
CREATE TABLE orchestration_v2_projection_threads (
  thread_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  title TEXT NOT NULL,
  default_provider TEXT NOT NULL,
  runtime_mode TEXT NOT NULL,
  interaction_mode TEXT NOT NULL,
  active_provider_thread_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT,
  deleted_at TEXT,
  payload_json TEXT NOT NULL,
  provider_instance_id TEXT
);
CREATE TABLE orchestration_v2_projection_runs (
  run_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  provider TEXT NOT NULL,
  provider_thread_id TEXT,
  status TEXT NOT NULL,
  requested_at TEXT NOT NULL,
  completed_at TEXT,
  payload_json TEXT NOT NULL,
  provider_instance_id TEXT
);
CREATE TABLE orchestration_v2_projection_provider_sessions (
  provider_session_id TEXT PRIMARY KEY,
  thread_id TEXT,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  model TEXT,
  updated_at TEXT NOT NULL,
  payload_json TEXT NOT NULL
);
CREATE TABLE orchestration_v2_projection_runtime_requests (
  runtime_request_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  provider_turn_id TEXT,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  resolved_at TEXT,
  payload_json TEXT NOT NULL
);
