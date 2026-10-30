package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	tuiOriginator     = "codex-tui"
	replyRetryAfter   = 5 * time.Minute
	maxPendingReplies = 2000
)

var (
	userRoleMarker = []byte(`"role":"user"`)
	replyFailLog   = producer.NewFailureLogger(time.Hour)
)

func promptAt(line []byte) (time.Time, bool) {
	if !bytes.Contains(line, userRoleMarker) {
		return time.Time{}, false
	}
	var rl rolloutLine
	if json.Unmarshal(line, &rl) != nil || rl.Type != "response_item" {
		return time.Time{}, false
	}
	var p struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(rl.Payload, &p) != nil || p.Type != "message" || p.Role != "user" {
		return time.Time{}, false
	}
	text := ""
	for _, c := range p.Content {
		if c.Type == "input_text" {
			text = strings.TrimLeft(c.Text, " \t\r\n")
			break
		}
	}
	if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "<") {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, rl.Timestamp)
	return at, err == nil
}

func (w *watcher) notePrompts(ss *sessionState, lines [][]byte) {
	if !ss.tui {
		return
	}
	for _, ln := range lines {
		if at, ok := promptAt(ln); ok {
			w.replies = append(w.replies, producer.Reply{At: at.Unix(), Source: w.cfg.Source, Tool: "codex", Session: ss.uuid, Repo: ss.repo})
		}
	}
	if n := len(w.replies); n > maxPendingReplies {
		w.replies = w.replies[n-maxPendingReplies:]
	}
}

func (w *watcher) sendReplies(ctx context.Context, client *producer.Client) {
	now := w.now()
	if len(w.replies) == 0 || now.Before(w.nextReplies) {
		return
	}
	if err := client.Replies(ctx, w.replies); err != nil {
		replyFailLog.Warn(slog.Default(), "codex_replies", "day replies POST failed", "err", err)
		w.nextReplies = now.Add(replyRetryAfter)
		return
	}
	w.replies = nil
}
