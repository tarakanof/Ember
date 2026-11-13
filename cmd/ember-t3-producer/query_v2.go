package main

import "strings"

const t3AwarenessMigration = 68

func sqlAt(tmpl, alias string) string {
	return strings.ReplaceAll(tmpl, "{a}", alias)
}

const idleCompactionSQL = `({a}.role = 'user'
    AND lower(trim(json_extract({a}.payload_json, '$.text'),
      char(9, 10, 11, 12, 13, 32, 160, 5760, 8192, 8193, 8194, 8195,
        8196, 8197, 8198, 8199, 8200, 8201, 8202, 8232, 8233, 8239, 8287, 12288, 65279)
    )) = '/compact'
    AND coalesce(json_array_length({a}.payload_json, '$.attachments'), 0) = 0
    AND (
      json_extract({a}.payload_json, '$.idleCompaction') IS NOT NULL
      OR (
        json_extract({a}.payload_json, '$.createdBy') = 'agent'
        AND json_extract({a}.payload_json, '$.creationSource') = 'server'
      )
    ))`

var silentRunSQL = `({a}.status <> 'failed'
    AND EXISTS (SELECT 1 FROM orchestration_v2_projection_messages {a}_prompt
      WHERE {a}_prompt.message_id = json_extract({a}.payload_json, '$.userMessageId')
        AND ` + sqlAt(idleCompactionSQL, "{a}_prompt") + `)
    AND NOT EXISTS (SELECT 1 FROM orchestration_v2_projection_turn_items {a}_compaction
      WHERE {a}_compaction.thread_id = {a}.thread_id AND {a}_compaction.run_id = {a}.run_id
        AND {a}_compaction.type = 'compaction' AND {a}_compaction.status = 'failed'))`

const backgroundWakeSQL = `(EXISTS (SELECT 1 FROM orchestration_v2_projection_messages wake
      WHERE wake.message_id = json_extract({a}.payload_json, '$.userMessageId')
        AND json_extract(wake.payload_json, '$.notification.source.kind')
          IN ('background_task', 'background_command', 'command', 'monitor')
        AND json_extract(wake.payload_json, '$.notification.source.work') IS NULL
        AND json_extract(wake.payload_json, '$.notification.outcome') = 'updated'
        AND json_extract(wake.payload_json, '$.delegatedCompletion') IS NULL)
    AND NOT EXISTS (SELECT 1 FROM orchestration_v2_projection_messages steer
      WHERE steer.run_id = {a}.run_id
        AND json_extract(steer.payload_json, '$.createdBy') = 'user'))`

const rosterSQL = `orchestration_v2_projection_provider_threads {a}_pt,
        json_each({a}_pt.payload_json, '$.pendingBackgroundTasks') {a}
      WHERE {a}_pt.thread_id = f.thread_id
        AND (json_extract(f.payload_json, '$.activeProviderThreadId') IS NULL
          OR {a}_pt.provider_thread_id = json_extract(f.payload_json, '$.activeProviderThreadId'))
        AND length(COALESCE(json_extract({a}.value, '$.taskId'), '')) > 0`

const backgroundItemSQL = `orchestration_v2_projection_turn_items i
      LEFT JOIN orchestration_v2_projection_runs ir ON ir.run_id = i.run_id
      -- T3's turn_items_recovery_idx is partial on exactly this type list and
      -- status list; SQLite only uses it when the WHERE repeats them verbatim.
      WHERE i.thread_id = f.thread_id
        AND i.type IN ('command_execution', 'dynamic_tool', 'subagent')
        AND i.status IN ('pending', 'running', 'waiting')
        AND (i.run_id IS NULL OR ir.status <> 'rolled_back')`

const pendingRequestOrder = `(q.kind = 'user_input') ASC, q.created_at DESC, q.runtime_request_id DESC`

var queryV2 = `
WITH RECURSIVE secret AS (
  SELECT thread_id, node_id, updated_at, stamp FROM (
    SELECT s.thread_id, s.node_id, s.updated_at,
      COALESCE(json_extract(s.payload_json, '$.startedAt'), s.updated_at) AS stamp,
      row_number() OVER (PARTITION BY s.thread_id ORDER BY s.updated_at DESC, s.turn_item_id DESC) AS rn
    FROM orchestration_v2_projection_runs r
    CROSS JOIN orchestration_v2_projection_turn_items s
    WHERE r.status IN ('queued', 'preparing', 'starting', 'running', 'waiting') AND r.status <> 'queued'
      AND s.thread_id = r.thread_id AND s.run_id = r.run_id
      AND s.type = 'secret_request' AND s.status = 'waiting')
  WHERE rn = 1),
link AS (
  SELECT thread_id, node_id, updated_at, stamp FROM (
    SELECT l.thread_id, l.node_id, l.updated_at,
      COALESCE(json_extract(l.payload_json, '$.startedAt'), l.updated_at) AS stamp,
      row_number() OVER (PARTITION BY l.thread_id ORDER BY l.updated_at DESC, l.turn_item_id DESC) AS rn
    FROM orchestration_v2_projection_turn_items l
    WHERE l.type = 'link_request' AND l.status = 'waiting')
  WHERE rn = 1),
card AS (
  SELECT thread_id, node_id, stamp FROM (
    SELECT thread_id, node_id, stamp,
      row_number() OVER (PARTITION BY thread_id ORDER BY julianday(updated_at) DESC, source) AS rn
    FROM (
      SELECT thread_id, node_id, updated_at, stamp, 0 AS source FROM secret
      UNION ALL
      SELECT k.thread_id, k.node_id, k.updated_at, k.stamp, 1 FROM link k
      WHERE NOT EXISTS (SELECT 1 FROM orchestration_v2_projection_messages m
        WHERE m.thread_id = k.thread_id AND m.role = 'user'
          AND json_extract(m.payload_json, '$.createdBy') = 'user'
          AND NOT ` + sqlAt(idleCompactionSQL, "m") + `
          AND julianday(m.updated_at) > julianday(k.stamp))))
  WHERE rn = 1),
child_ask AS (
  SELECT c.thread_id, json_extract(c.payload_json, '$.lineage.parentThreadId') AS parent_id, q.kind, q.created_at
  FROM (
    SELECT q.thread_id, q.kind, q.created_at,
      row_number() OVER (PARTITION BY q.thread_id ORDER BY ` + pendingRequestOrder + `) AS rn
    FROM orchestration_v2_projection_runtime_requests q
    WHERE q.status = 'pending') q
  JOIN orchestration_v2_projection_threads c ON c.thread_id = q.thread_id
  WHERE q.rn = 1 AND q.kind NOT IN ('auth_refresh', 'user_input')
    AND c.deleted_at IS NULL
    AND json_extract(c.payload_json, '$.lineage.relationshipToParent') = 'subagent'
    AND json_extract(c.payload_json, '$.creationSource') IS NOT 'provider'
    AND COALESCE(c.archived_at, json_extract(c.payload_json, '$.archivedAt')) IS NULL
    AND EXISTS (SELECT 1 FROM orchestration_v2_projection_runs cr
      WHERE cr.thread_id = c.thread_id AND cr.status IN ('preparing', 'starting', 'running', 'waiting')
        AND NOT ` + sqlAt(silentRunSQL, "cr") + `)),
ancestry(cur_id, kind, created_at, depth) AS (
  SELECT parent_id, kind, created_at, 0 FROM child_ask
  UNION ALL
  SELECT json_extract(p.payload_json, '$.lineage.parentThreadId'), a.kind, a.created_at, a.depth + 1
  FROM ancestry a
  JOIN orchestration_v2_projection_threads p ON p.thread_id = a.cur_id
  WHERE p.deleted_at IS NULL AND a.depth < 32
    AND (json_extract(p.payload_json, '$.lineage.relationshipToParent') IS 'subagent'
      OR json_extract(p.payload_json, '$.lineage.sideChat') IS 1)),
subagent_ask AS (
  SELECT top_id, kind, created_at FROM (
    SELECT a.cur_id AS top_id, a.kind, a.created_at,
      row_number() OVER (PARTITION BY a.cur_id ORDER BY a.created_at DESC) AS rn
    FROM ancestry a
    JOIN orchestration_v2_projection_threads top ON top.thread_id = a.cur_id
    WHERE top.deleted_at IS NULL
      AND json_extract(top.payload_json, '$.lineage.relationshipToParent') IS NOT 'subagent'
      AND json_extract(top.payload_json, '$.lineage.sideChat') IS NOT 1)
  WHERE rn = 1),
base AS (
  SELECT t.thread_id, t.title, t.payload_json, t.archived_at,
    (SELECT json_extract(ps.payload_json, '$.lastError') FROM orchestration_v2_projection_provider_session_bindings b
      -- CROSS JOIN keeps bindings outermost: one provider instance can hold every session.
      CROSS JOIN orchestration_v2_projection_provider_sessions ps
        ON ps.provider_session_id = b.provider_session_id
      WHERE b.thread_id = t.thread_id AND ps.provider_instance_id = t.provider_instance_id
      ORDER BY ps.updated_at DESC, ps.provider_session_id DESC LIMIT 1) AS session_error,
    (SELECT c.run_id FROM orchestration_v2_projection_runs c
      WHERE c.thread_id = t.thread_id
        AND NOT (c.status = 'queued' AND json_extract(c.payload_json, '$.queueHeld') IS 1)
        AND NOT ` + sqlAt(silentRunSQL, "c") + `
      ORDER BY c.ordinal DESC, c.run_id DESC LIMIT 1) AS presented_id,
    (SELECT x.run_id FROM orchestration_v2_projection_runs x
      WHERE x.thread_id = t.thread_id AND x.status <> 'queued'
        AND NOT (x.status = 'cancelled' AND json_extract(x.payload_json, '$.startedAt') IS NULL)
      ORDER BY x.completed_at IS NULL DESC, x.completed_at DESC, x.ordinal DESC, x.run_id DESC
      LIMIT 1) AS executed_id
  FROM orchestration_v2_projection_threads t
  WHERE t.deleted_at IS NULL
    AND json_extract(t.payload_json, '$.lineage.relationshipToParent') IS NOT 'subagent'
    AND json_extract(t.payload_json, '$.lineage.sideChat') IS NOT 1),
shown AS (
  SELECT base.*, COALESCE(
    (SELECT x.run_id FROM orchestration_v2_projection_runs x
      WHERE x.run_id = base.executed_id AND x.status = 'failed'
        AND (SELECT json_extract(i.payload_json, '$.failure.class') IS 'usage_limit'
            AND (base.session_error IS NULL
              OR base.session_error IS json_extract(i.payload_json, '$.failure.message'))
          FROM orchestration_v2_projection_turn_items i
          WHERE i.thread_id = base.thread_id AND i.run_id = x.run_id
            AND i.type = 'error' AND i.status = 'failed'
            AND i.node_id IS json_extract(x.payload_json, '$.rootNodeId')
          ORDER BY i.updated_at DESC, i.ordinal DESC, i.turn_item_id DESC LIMIT 1) IS 1),
    base.presented_id) AS shown_id
  FROM base),
facts AS (
  SELECT f.*, presented.status, presented.requested_at, presented.completed_at,
    presented.payload_json AS presented_payload, presented.run_id AS presented_run_id,
    CASE WHEN presented.status IN ('cancelled', 'completed', 'failed', 'interrupted', 'waiting') THEN (
      EXISTS (SELECT 1 FROM ` + sqlAt(rosterSQL, "task") + `
          AND (json_extract(task.value, '$.awaited') IS 1
            OR json_extract(task.value, '$.kind') IN ('subagent', 'background_task')))
      OR EXISTS (SELECT 1 FROM ` + backgroundItemSQL + `
          AND i.type <> 'command_execution'
          AND NOT (i.type = 'dynamic_tool' AND json_type(i.payload_json, '$.input.persistent') IS 'true')
          AND NOT EXISTS (SELECT 1 FROM ` + sqlAt(rosterSQL, "dup") + `
            AND json_extract(dup.value, '$.taskId') = COALESCE(
              NULLIF(json_extract(i.payload_json, '$.nativeItemRef.nativeId'), ''), i.turn_item_id)))
      OR EXISTS (SELECT 1 FROM json_each(f.payload_json, '$.pullRequests') pr
        WHERE json_extract(pr.value, '$.watch') IS NOT NULL
          AND json_extract(pr.value, '$.source') IS NOT 'stack-dismissed'
          AND json_extract(pr.value, '$.snapshot.state') IS NOT 'merged')
    ) ELSE 0 END AS work_hold,
    CASE WHEN presented.status = 'completed' AND ` + sqlAt(backgroundWakeSQL, "presented") + ` THEN (
      EXISTS (SELECT 1 FROM ` + sqlAt(rosterSQL, "mon") + `
          AND json_extract(mon.value, '$.kind') IS 'monitor')
      OR EXISTS (SELECT 1 FROM ` + backgroundItemSQL + `
          AND i.type = 'command_execution'
          AND json_extract(i.payload_json, '$.monitor') IS 1)
    ) ELSE 0 END AS wake_hold,
    EXISTS (SELECT 1 FROM orchestration_v2_projection_turn_items stop
      WHERE stop.thread_id = f.thread_id AND stop.run_id = presented.run_id
        AND stop.type = 'run_interrupt_request') AS stop_requested
  FROM shown f
  LEFT JOIN orchestration_v2_projection_runs presented ON presented.run_id = f.shown_id),
judged AS (
  SELECT facts.*,
    (json_extract(payload_json, '$.settledOverride') IS 'settled' AND NOT work_hold)
      OR (status = 'completed' AND stop_requested AND NOT (work_hold OR wake_hold)) AS hidden
  FROM facts)
SELECT
  j.thread_id,
  j.title,
  j.status,
  COALESCE(
    CASE WHEN card.thread_id IS NOT NULL THEN 'user_input' END,
    (SELECT q.kind FROM orchestration_v2_projection_runtime_requests q
      WHERE q.thread_id = j.thread_id AND q.status = 'pending' AND q.kind <> 'auth_refresh'
      ORDER BY ` + pendingRequestOrder + ` LIMIT 1),
    ask.kind),
  COALESCE(
    j.session_error,
    (SELECT json_extract(i.payload_json, '$.failure.message') FROM orchestration_v2_projection_turn_items i
      WHERE j.status = 'failed' AND i.run_id = j.presented_run_id AND i.thread_id = j.thread_id
        AND i.type = 'error' AND i.status = 'failed'
        AND i.node_id IS json_extract(j.presented_payload, '$.rootNodeId')
      ORDER BY i.updated_at DESC, i.ordinal DESC, i.turn_item_id DESC LIMIT 1)),
  COALESCE(j.archived_at, json_extract(j.payload_json, '$.archivedAt')),
  j.requested_at,
  j.completed_at,
  COALESCE(
    card.stamp,
    (SELECT max(q.created_at) FROM orchestration_v2_projection_runtime_requests q
      WHERE q.thread_id = j.thread_id AND q.status = 'pending' AND q.kind <> 'auth_refresh'),
    ask.created_at),
  (SELECT a.status FROM orchestration_v2_projection_runs a
    WHERE a.thread_id = j.thread_id AND a.status IN ('preparing', 'starting', 'running', 'waiting')
      AND NOT ` + sqlAt(silentRunSQL, "a") + `
    ORDER BY a.ordinal DESC, a.run_id DESC LIMIT 1),
  j.status = 'completed' AND (j.work_hold OR j.wake_hold) AND NOT j.hidden,
  0,
  j.hidden
FROM judged j
LEFT JOIN card ON card.thread_id = j.thread_id AND card.node_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM orchestration_v2_projection_runtime_requests q
    WHERE q.thread_id = j.thread_id AND q.status = 'pending'
      AND (q.kind <> 'user_input' OR julianday(q.created_at) > julianday(card.stamp)))
LEFT JOIN subagent_ask ask ON ask.top_id = j.thread_id`
