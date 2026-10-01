package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

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
	IsInterrupt         bool            `json:"is_interrupt,omitempty"`
	ToolUseID           string          `json:"tool_use_id,omitempty"`
	EndReason           string          `json:"end_reason,omitempty"`
}

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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
	defer cancel()
	dispatchHookFrom(ctx, event, io.LimitReader(os.Stdin, hookStdinMax), cfg)
	os.Exit(0)
}

const hookStdinMax = 64 << 20

func decodeHookInput(r io.Reader) (hookInput, error) {
	var in hookInput
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return in, fmt.Errorf("hook stdin is not a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return in, err
		}
		key, _ := t.(string)
		if key == "tool_response" {
			if err := skipJSONValue(dec); err != nil {
				return in, err
			}
			continue
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return in, err
		}
		fields[key] = raw
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return in, err
	}
	err = json.Unmarshal(body, &in)
	return in, err
}

func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func dispatchHook(ctx context.Context, event string, stdin []byte, cfg Config) {
	dispatchHookFrom(ctx, event, bytes.NewReader(stdin), cfg)
}

func dispatchHookFrom(ctx context.Context, event string, r io.Reader, cfg Config) {
	in, err := decodeHookInput(r)
	if err != nil {
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
	case "post-tool-use":
		handleToolOutcome(ctx, cfg, client, in, "", markerP, lockP)
	case "post-tool-use-failure":
		handleToolOutcome(ctx, cfg, client, in, failureOutcome(in), markerP, lockP)
	case "permission-denied":
		handleToolOutcome(ctx, cfg, client, in, "denied", markerP, lockP)
	case "session-start":
		handleSessionStart(in, markerP, lockP)
	case "user-prompt-submit":
		handleUpsert(ctx, cfg, client, sessionID, "running", truncate(in.Prompt, 80), "", markerP, lockP)
	case "pre-tool-use":
		act := ""
		if cfg.ActivityDetailEnabled {
			act = activityString(in.ToolName, in.ToolInput)
		}
		handleUpsertWith(ctx, cfg, client, sessionID, "running", in.ToolName, act, markerP, lockP, upsertExtra{
			preToolUseID: in.ToolUseID, preFP: permissionFingerprint(in.ToolName, in.ToolInput),
		})
	case "permission-request":
		act := ""
		if cfg.ActivityDetailEnabled {
			act = activityString(in.ToolName, in.ToolInput)
		}
		handleUpsertWith(ctx, cfg, client, sessionID, "waiting", "approve "+in.ToolName, act, markerP, lockP, upsertExtra{
			pending: permissionFingerprint(in.ToolName, in.ToolInput),
		})
	case "notification":
		msg := pickFirstNonEmpty(in.Message, in.NotificationMessage)
		switch in.NotificationType {
		case "permission_prompt":
			handleUpsertWith(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP, upsertExtra{
				skip: func(prev marker) bool { return lateResumedPrompt(prev, msg, hookNow()) },
			})
		case "agent_needs_input":
			handleUpsert(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP)
		case "agent_completed":
			handleUpsert(ctx, cfg, client, sessionID, "done", msg, "", markerP, lockP)
		}
	case "stop":
	case "stop-failure":
		msg := pickFirstNonEmpty(in.ErrorType, in.Error, in.ErrorMessage, "error")
		handleUpsert(ctx, cfg, client, sessionID, "error", msg, "", markerP, lockP)
	case "session-end":
		switch in.EndReason {
		case "logout", "prompt_input_exit", "bypass_permissions_disabled", "other", "clear":
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
	handleUpsertWith(ctx, cfg, client, sessionID, state, message, activity, markerP, lockP, upsertExtra{})
}

type upsertExtra struct {
	pending             string
	preToolUseID, preFP string
	skip                func(prev marker) bool
}

func handleUpsertWith(ctx context.Context, cfg Config, client *Client, sessionID, state, message, activity, markerP, lockP string, x upsertExtra) {
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
		var ownerPID int
		var ownerStart string
		var track ToolTrack
		if old, err := readMarker(markerP); err == nil {
			var prev marker
			if json.Unmarshal(old, &prev) == nil {
				if x.skip != nil && x.skip(prev) {
					return nil
				}
				track = prev.ToolTrack
				req.RateWindowPct = prev.RateWindowPct
				req.RateResetAt = prev.RateResetAt
				req.RateResetLabel = prev.RateResetLabel
				req.RateWeekPct = prev.RateWeekPct
				req.RateWeekResetAt = prev.RateWeekResetAt
				req.RateWeekResetLabel = prev.RateWeekResetLabel
				if cfg.ContextPctEnabled {
					req.ContextPct = prev.ContextPct
				}
				if cfg.ActivityTrailEnabled && activity != "" {
					req.Activity = producer.PrependTrail(activity, prev.Activity)
				}
				ownerPID, ownerStart = prev.OwnerPID, prev.OwnerStart
			}
		}
		if x.preFP != "" {
			track.LastToolUseID, track.LastToolFP = x.preToolUseID, x.preFP
		}
		if x.pending != "" {
			track.PendingPermission, track.PendingToolUseID = x.pending, ""
			if track.LastToolFP == x.pending {
				track.PendingToolUseID = track.LastToolUseID
			}
			track.ResumedTool, track.ResumedAt = "", 0
		}
		if state != "waiting" {
			track.PendingPermission, track.PendingToolUseID = "", ""
		}
		if ownerPID == 0 {
			ownerPID, ownerStart = detectOwner()
		}
		m := marker{StatusRequest: req, OwnerPID: ownerPID, OwnerStart: ownerStart, ToolTrack: track}
		body, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		_ = writeMarker(markerP, body)
		_ = client.Post(ctx, wireRequest(cfg, req))
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
	return producer.Truncate(s, n)
}

func pickFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
