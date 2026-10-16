-- Subset of T3 Code v0.1.0 <T3CODE_HOME>/userdata/statev2.sqlite (migrations 55-62),
-- transcribed from apps/server/src/persistence/Migrations/055_OrchestrationV2.ts, its
-- OrchestrationV2/*.ts steps, 060 and 062: the tables ember-t3-producer reads and every index on them.
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
  payload_json TEXT NOT NULL,
  driver TEXT,
  provider_instance_id TEXT
);
CREATE TABLE orchestration_v2_projection_provider_session_bindings (
  provider_session_id TEXT NOT NULL,
  thread_id TEXT NOT NULL,
  PRIMARY KEY (provider_session_id, thread_id)
);
CREATE TABLE orchestration_v2_projection_provider_threads (
  provider_thread_id TEXT PRIMARY KEY,
  thread_id TEXT,
  owner_node_id TEXT,
  provider TEXT NOT NULL,
  provider_session_id TEXT,
  status TEXT NOT NULL,
  first_run_ordinal INTEGER,
  last_run_ordinal INTEGER,
  updated_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  driver TEXT,
  provider_instance_id TEXT
);
CREATE TABLE orchestration_v2_projection_turn_items (
  turn_item_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  run_id TEXT,
  node_id TEXT,
  provider_thread_id TEXT,
  provider_turn_id TEXT,
  parent_item_id TEXT,
  ordinal INTEGER NOT NULL,
  type TEXT NOT NULL,
  status TEXT NOT NULL,
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
CREATE INDEX orchestration_v2_projection_threads_project_updated_idx
  ON orchestration_v2_projection_threads(project_id, updated_at);
CREATE UNIQUE INDEX orchestration_v2_projection_runs_thread_ordinal_idx
  ON orchestration_v2_projection_runs(thread_id, ordinal);
CREATE INDEX orchestration_v2_projection_runs_provider_thread_idx
  ON orchestration_v2_projection_runs(provider_thread_id);
CREATE INDEX orchestration_v2_projection_runs_thread_status_idx
  ON orchestration_v2_projection_runs(thread_id, status);
CREATE INDEX orchestration_v2_projection_provider_sessions_thread_idx
  ON orchestration_v2_projection_provider_sessions(thread_id);
CREATE INDEX orchestration_v2_projection_provider_sessions_provider_status_idx
  ON orchestration_v2_projection_provider_sessions(provider, status);
CREATE INDEX orchestration_v2_projection_provider_threads_thread_idx
  ON orchestration_v2_projection_provider_threads(thread_id);
CREATE INDEX orchestration_v2_projection_provider_threads_session_idx
  ON orchestration_v2_projection_provider_threads(provider_session_id);
CREATE INDEX orchestration_v2_projection_provider_threads_owner_idx
  ON orchestration_v2_projection_provider_threads(owner_node_id);
CREATE INDEX orchestration_v2_projection_runtime_requests_thread_status_idx
  ON orchestration_v2_projection_runtime_requests(thread_id, status);
CREATE INDEX orchestration_v2_projection_runtime_requests_provider_turn_idx
  ON orchestration_v2_projection_runtime_requests(provider_turn_id);
CREATE INDEX orchestration_v2_projection_turn_items_thread_ordinal_idx
  ON orchestration_v2_projection_turn_items(thread_id, ordinal, turn_item_id);
CREATE INDEX orchestration_v2_projection_turn_items_run_ordinal_idx
  ON orchestration_v2_projection_turn_items(run_id, ordinal);
CREATE INDEX orchestration_v2_projection_turn_items_node_ordinal_idx
  ON orchestration_v2_projection_turn_items(node_id, ordinal);
CREATE INDEX orchestration_v2_projection_turn_items_provider_turn_idx
  ON orchestration_v2_projection_turn_items(provider_turn_id);
CREATE INDEX orchestration_v2_projection_provider_sessions_instance_status_idx
  ON orchestration_v2_projection_provider_sessions(provider_instance_id, status);
CREATE INDEX orchestration_v2_projection_provider_threads_instance_status_idx
  ON orchestration_v2_projection_provider_threads(provider_instance_id, status);
CREATE INDEX orchestration_v2_projection_provider_session_bindings_thread_idx
  ON orchestration_v2_projection_provider_session_bindings(thread_id);
CREATE INDEX orchestration_v2_projection_runs_recovery_idx
  ON orchestration_v2_projection_runs(status, thread_id)
  WHERE status IN ('queued', 'preparing', 'starting', 'running', 'waiting');
CREATE INDEX orchestration_v2_projection_requests_recovery_idx
  ON orchestration_v2_projection_runtime_requests(thread_id)
  WHERE status = 'pending';
CREATE INDEX orchestration_v2_projection_turn_items_recovery_idx
  ON orchestration_v2_projection_turn_items(thread_id)
  WHERE type IN ('command_execution', 'dynamic_tool', 'subagent')
    AND status IN ('pending', 'running', 'waiting');
CREATE INDEX orchestration_v2_projection_turn_items_thread_run_idx
  ON orchestration_v2_projection_turn_items(thread_id, run_id);
CREATE INDEX orchestration_v2_projection_turn_items_shell_pending_idx
  ON orchestration_v2_projection_turn_items(thread_id, run_id)
  WHERE type IN ('command_execution', 'dynamic_tool', 'subagent')
    AND status NOT IN ('completed', 'interrupted', 'failed', 'cancelled');
CREATE INDEX orchestration_v2_projection_turn_items_user_message_idx
  ON orchestration_v2_projection_turn_items(thread_id, ordinal, turn_item_id)
  WHERE type = 'user_message';
CREATE INDEX orchestration_v2_projection_turn_items_failed_error_idx
  ON orchestration_v2_projection_turn_items(thread_id, run_id, node_id, updated_at DESC, ordinal DESC, turn_item_id DESC)
  WHERE type = 'error'
    AND status = 'failed';
