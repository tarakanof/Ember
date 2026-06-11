package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// hookInput is the union of fields we read from any Claude Code hook's stdin.
// Field-name pairs (e.g., "notification_message" vs "message") cover both
// documented and disputed variants — handlers fall back gracefully.
type hookInput struct {
	HookEventName       string          `json:"hook_event_name"`
	SessionID           string          `json:"session_id"`
	CWD                 string          `json:"cwd"`
	Source              string          `json:"source,omitempty"`
	Prompt              string          `json:"prompt,omitempty"`
	ToolName            string          `json:"tool_name,omitempty"`
	ToolInput           json.RawMessage `json:"tool_input"`
	NotificationType    string          `json:"notification_type,omitempty"`
	NotificationMessage string          `json:"notification_message,omitempty"`
	Message             string          `json:"message,omitempty"`
	ErrorType           string          `json:"error_type,omitempty"`
	ErrorMessage        string          `json:"error_message,omitempty"`
	Error               string          `json:"error,omitempty"`
	EndReason           string          `json:"end_reason,omitempty"`
}

// runHook is the entry point for `ember-claude-producer hook <event>`.
// Always exits 0 — we never want to break the user's claude CLI.
func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}
	rotateProducerLogs()
	event := args[0]
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		os.Exit(0)
	}
	stdin, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
	defer cancel()
	dispatchHook(ctx, event, stdin, cfg)
	os.Exit(0)
}

// dispatchHook is the testable seam: parses stdin JSON, performs marker mutation
// + HTTP, swallows all errors.
func dispatchHook(ctx context.Context, event string, stdin []byte, cfg Config) {
	var in hookInput
	if err := json.NewDecoder(bytes.NewReader(stdin)).Decode(&in); err != nil {
		return
	}
	sessionID := sanitizeSessionID(in.SessionID, in.CWD)
	dir, err := stateDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	markerP := markerPath(dir, sessionID)
	lockP := lockPath(dir, sessionID)
	client := NewClient(cfg)
	switch event {
	case "session-start":
		handleSessionStart(in, markerP, lockP)
	case "user-prompt-submit":
		handleUpsert(ctx, cfg, client, sessionID, "running", truncate(in.Prompt, 80), "", markerP, lockP)
	case "pre-tool-use":
		act := ""
		if cfg.ActivityDetailEnabled {
			act = activityString(in.ToolName, in.ToolInput)
		}
		handleUpsert(ctx, cfg, client, sessionID, "running", in.ToolName, act, markerP, lockP)
	case "permission-request":
		act := ""
		if cfg.ActivityDetailEnabled {
			act = activityString(in.ToolName, in.ToolInput)
		}
		handleUpsert(ctx, cfg, client, sessionID, "waiting", "approve "+in.ToolName, act, markerP, lockP)
	case "notification":
		if in.NotificationType != "permission_prompt" {
			return
		}
		msg := pickFirstNonEmpty(in.Message, in.NotificationMessage)
		handleUpsert(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP)
	case "stop":
		// Intentionally a no-op: keep the session present until the window
		// closes (SessionEnd). Deleting on every Stop dropped the display to the
		// idle robot between turns and during text generation, when no hook
		// fires. The marker keeps its last state ("running") and the heartbeat
		// tick re-posts it; SessionEnd clears it (or the marker TTL, for a window
		// that closed without a clean SessionEnd).
	case "stop-failure":
		msg := pickFirstNonEmpty(in.ErrorType, in.Error, in.ErrorMessage, "error")
		handleUpsert(ctx, cfg, client, sessionID, "error", msg, "", markerP, lockP)
	case "session-end":
		switch in.EndReason {
		case "logout", "prompt_input_exit", "bypass_permissions_disabled", "other":
			handleDelete(ctx, cfg, client, sessionID, markerP, lockP)
		}
	}
}

func handleSessionStart(in hookInput, markerP, lockP string) {
	if in.Source == "startup" {
		return
	}
	_ = withLockEx(lockP, func() error {
		_ = os.Remove(markerP)
		return nil
	})
}

func handleUpsert(ctx context.Context, cfg Config, client *Client, sessionID, state, message, activity, markerP, lockP string) {
	req := StatusRequest{
		Source:        cfg.Source,
		Tool:          "claude",
		Session:       sessionID,
		State:         state,
		Message:       truncate(message, 80),
		Activity:      truncate(activity, 80),
		ContextNumber: cfg.ContextNumberEnabled,
		RateBottomBar: cfg.RateBottomBarEnabled,
		RateReset:     cfg.RateResetEnabled,
	}
	if cfg.SourceColor != "" {
		sc := cfg.SourceColor
		req.SourceColor = &sc
	}
	sc, sb := cfg.SourceCardEnabled, cfg.SessionBarEnabled
	req.SourceCard, req.SessionBar = &sc, &sb
	_ = withLockEx(lockP, func() error {
		// Preserve statusline-owned fields (rate_window_pct, context_pct) that
		// the hook path doesn't compute, so a hook event doesn't clobber the
		// statusline's enrichment of this marker.
		var ownerPID int
		var ownerStart string
		if old, err := readMarker(markerP); err == nil {
			var prev marker
			if json.Unmarshal(old, &prev) == nil {
				req.RateWindowPct = prev.RateWindowPct
				req.RateResetAt = prev.RateResetAt
				req.RateResetLabel = prev.RateResetLabel
				if cfg.ContextPctEnabled {
					req.ContextPct = prev.ContextPct
				}
				if cfg.ActivityTrailEnabled && activity != "" {
					req.Activity = producer.PrependTrail(activity, prev.Activity)
				}
				ownerPID, ownerStart = prev.OwnerPID, prev.OwnerStart
			}
		}
		// Capture the owning Claude process once per session (preserved across
		// later upserts), so the heartbeat can detect an ungraceful close.
		if ownerPID == 0 {
			ownerPID, ownerStart = detectOwner()
		}
		m := marker{StatusRequest: req, OwnerPID: ownerPID, OwnerStart: ownerStart}
		body, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		_ = writeMarker(markerP, body)
		_ = client.Post(ctx, req)
		return nil
	})
}

func handleDelete(ctx context.Context, cfg Config, client *Client, sessionID, markerP, lockP string) {
	_ = withLockEx(lockP, func() error {
		_ = os.Remove(markerP)
		_ = client.Delete(ctx, DeleteRequest{
			Source:  cfg.Source,
			Tool:    "claude",
			Session: sessionID,
		})
		return nil
	})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func pickFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
