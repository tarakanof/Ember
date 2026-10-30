package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	replyPollInterval = 10 * time.Second
	replyRetryAfter   = 5 * time.Minute
	replyFirstRunBack = 60 * 24 * time.Hour
	replyMaxBatches   = 20
	replyCursorFile   = "t3-replies.cursor"
	t3StampLayout     = "2006-01-02T15:04:05.000Z07:00"
)

var replyFailLog = producer.NewFailureLogger(time.Hour)

type replyCursor struct {
	At string `json:"at"`
	ID string `json:"id"`
}

type t3Prompt struct {
	ID, Thread, At, Head, Cwd string
}

const queryPromptsHead = `
SELECT m.message_id, m.thread_id, m.created_at,
  substr(COALESCE(json_extract(m.payload_json, '$.text'), ''), 1, 32),
  COALESCE(json_extract(t.payload_json, '$.worktreePath'), `

const queryPromptsTail = `, '')
FROM orchestration_v2_projection_messages m
LEFT JOIN orchestration_v2_projection_threads t ON t.thread_id = m.thread_id
WHERE m.role = 'user' AND m.streaming = 0
  AND json_extract(m.payload_json, '$.createdBy') = 'user'
  AND json_extract(t.payload_json, '$.lineage.relationshipToParent') IS NOT 'subagent'
  AND (m.created_at > ?1 OR (m.created_at = ?1 AND m.message_id > ?2))
ORDER BY m.created_at, m.message_id
LIMIT ?3`

const projectRoot = `(SELECT p.workspace_root FROM projection_projects p WHERE p.project_id = t.project_id)`

func (r *storeReader) prompts(ctx context.Context, home string, after replyCursor, limit int) ([]t3Prompt, error) {
	path := filepath.Join(home, "userdata", "statev2.sqlite")
	if _, ok := dbModTime(path); !ok {
		return nil, errNoDatabase
	}
	db, err := r.handle(path)
	if err != nil {
		return nil, err
	}
	root := "NULL"
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'projection_projects'`).Scan(&n); err != nil {
		r.Close()
		return nil, err
	}
	if n > 0 {
		root = projectRoot
	}
	rows, err := db.QueryContext(ctx, queryPromptsHead+root+queryPromptsTail, after.At, after.ID, limit)
	if err != nil {
		if strings.Contains(err.Error(), "no such column") || strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("%w: %v", errUnknownSchema, err)
		}
		r.Close()
		return nil, err
	}
	defer rows.Close()
	var out []t3Prompt
	for rows.Next() {
		var p t3Prompt
		if err := rows.Scan(&p.ID, &p.Thread, &p.At, &p.Head, &p.Cwd); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *daemon) replyCursorPath() string { return filepath.Join(d.cfg.StateDir, replyCursorFile) }

func (d *daemon) loadReplyCursor() replyCursor {
	var c replyCursor
	if b, err := os.ReadFile(d.replyCursorPath()); err == nil && json.Unmarshal(b, &c) == nil && c.At != "" {
		return c
	}
	return replyCursor{At: d.now().Add(-replyFirstRunBack).UTC().Format(t3StampLayout)}
}

func (d *daemon) saveReplyCursor(c replyCursor) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.cfg.StateDir, 0o700); err != nil {
		return err
	}
	return producer.WriteFileAtomic(d.replyCursorPath(), b, 0o600)
}

func (d *daemon) sendReplies(ctx context.Context) {
	now := d.now()
	if now.Before(d.nextReplies) {
		return
	}
	d.nextReplies = now.Add(replyPollInterval)
	cur := d.loadReplyCursor()
	for range replyMaxBatches {
		batch, err := d.prompts.prompts(ctx, d.cfg.T3Home, cur, producer.RepliesPerRequest)
		if err != nil {
			replyFailLog.Warn(slog.Default(), "t3_prompts", "reading T3 prompts failed", "err", err)
			d.nextReplies = now.Add(replyRetryAfter)
			return
		}
		if len(batch) == 0 {
			return
		}
		replies := d.promptReplies(batch)
		if len(replies) > 0 {
			if err := d.client.Replies(ctx, replies); err != nil {
				var se *producer.ServerError
				if errors.As(err, &se) && se.Code == 404 {
					err = fmt.Errorf("server has no day clock replies (older server or day clock off): %w", err)
				}
				replyFailLog.Warn(slog.Default(), "t3_replies", "day replies POST failed", "err", err)
				d.nextReplies = now.Add(replyRetryAfter)
				return
			}
		}
		last := batch[len(batch)-1]
		cur = replyCursor{At: last.At, ID: last.ID}
		if err := d.saveReplyCursor(cur); err != nil {
			replyFailLog.Warn(slog.Default(), "t3_cursor", "saving the T3 prompt cursor failed", "err", err)
			return
		}
		if len(batch) < producer.RepliesPerRequest {
			return
		}
	}
}

func (d *daemon) promptReplies(batch []t3Prompt) []producer.Reply {
	out := make([]producer.Reply, 0, len(batch))
	for _, p := range batch {
		if producer.AutomatedPrompt(p.Head) {
			continue
		}
		at, ok := parseStamp(p.At)
		if !ok {
			continue
		}
		out = append(out, producer.Reply{At: at.Unix(), Source: d.cfg.Source, Tool: toolName, Session: p.Thread,
			Repo: d.cfg.Project(producer.RepoFromCwd(p.Cwd))})
	}
	return out
}
