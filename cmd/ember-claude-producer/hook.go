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
	IsInterrupt         bool            `json:"is_interrupt,omitempty"`
	ToolUseID           string          `json:"tool_use_id,omitempty"`
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.HookTimeoutMs)*time.Millisecond)
	defer cancel()
	dispatchHookFrom(ctx, event, io.LimitReader(os.Stdin, hookStdinMax), cfg)
	os.Exit(0)
}

// hookStdinMax bounds what a hook reads from stdin. PostToolUse carries the
// tool's whole result in tool_response (a Read can be megabytes) and
// tool_use_id comes after it, so the old 1 MiB cap cut those payloads off and
// the hook did nothing. decodeHookInput streams past tool_response instead of
// holding the payload in memory.
const hookStdinMax = 64 << 20

// decodeHookInput stream-decodes a hook's stdin object. tool_response is
// skipped token by token and never stored: the producer must never forward a
// tool's output. Every other top-level field is small and decoded as usual.
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

// skipJSONValue consumes one JSON value from dec without keeping it.
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

// dispatchHook is the testable seam: parses stdin JSON, performs marker mutation
// + HTTP, swallows all errors.
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
	// Tool-outcome hooks (#76): see posttool.go. No new states.
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
		// permission_prompt and agent_needs_input are both explicit "waiting for
		// the user" signals (issue #75); agent_completed is an explicit "finished"
		// signal that upserts "done" rather than deleting — the process-ancestry
		// walk (owner.go) and SessionEnd remain the source of truth for clearing
		// a session, this only adds a faster signal on top.
		msg := pickFirstNonEmpty(in.Message, in.NotificationMessage)
		switch in.NotificationType {
		case "permission_prompt":
			// The dialog's ~6 s notification can land after its call already
			// ran (approved); lateResumedPrompt drops it then.
			handleUpsertWith(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP, upsertExtra{
				skip: func(prev marker) bool { return lateResumedPrompt(prev, msg, hookNow()) },
			})
		case "agent_needs_input":
			handleUpsert(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP)
		case "agent_completed":
			handleUpsert(ctx, cfg, client, sessionID, "done", msg, "", markerP, lockP)
		}
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

// upsertExtra carries the tool-outcome bookkeeping (#76) into an upsert.
type upsertExtra struct {
	// pending is a PermissionRequest's call fingerprint: the "waiting" this
	// upsert starts is for that call.
	pending string
	// preToolUseID / preFP identify a PreToolUse's call.
	preToolUseID, preFP string
	// skip, when it returns true for the existing marker, drops the upsert
	// (no write, no POST).
	skip func(prev marker) bool
}

// handleUpsertWith is handleUpsert plus the ToolTrack rules: a PermissionRequest
// records its pending call (and the PreToolUse id when the fingerprints
// match) and clears the last resume; a later "waiting" without one (the same
// dialog's Notification) keeps the pending call; any other state clears it.
// The last PreToolUse and the last resume survive other upserts.
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
		// Preserve statusline-owned fields (rate_window_pct, context_pct, and
		// their weekly counterparts) that the hook path doesn't compute, so a
		// hook event doesn't clobber the statusline's enrichment of this marker.
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
		// Capture the owning Claude process once per session (preserved across
		// later upserts), so the heartbeat can detect an ungraceful close.
		if ownerPID == 0 {
			ownerPID, ownerStart = detectOwner()
		}
		m := marker{StatusRequest: req, OwnerPID: ownerPID, OwnerStart: ownerStart, ToolTrack: track}
		body, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		_ = writeMarker(markerP, body)
		// The weekly fields are marker-only (relayed to POST /v1/usage by the
		// heartbeat tick, see tick.go's postStatuslineUsage); wireRequest
		// strips them so they never appear on this wire body.
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

// truncate is rune-safe: see producer.Truncate.
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
