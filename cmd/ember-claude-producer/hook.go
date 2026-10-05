package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type hookInput struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	CWD                  string          `json:"cwd"`
	Source               string          `json:"source,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	ToolName             string          `json:"tool_name,omitempty"`
	ToolInput            json.RawMessage `json:"tool_input"`
	NotificationType     string          `json:"notification_type,omitempty"`
	Message              string          `json:"message,omitempty"`
	Error                string          `json:"error,omitempty"`
	ErrorDetails         string          `json:"error_details,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
	BackgroundTasks      json.RawMessage `json:"background_tasks,omitempty"`
	IsInterrupt          bool            `json:"is_interrupt,omitempty"`
	ToolUseID            string          `json:"tool_use_id,omitempty"`
	EndReason            string          `json:"reason,omitempty"`
}

func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}
	if home, err := os.UserHomeDir(); err != nil || !hooksEnabledAt(home) {
		os.Exit(0)
	}
	producer.RotateLogs(producerLogs...)
	event := args[0]
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		os.Exit(0)
	}
	// The HTTP client caps the request at HookTimeoutMs; the context also
	// covers a bounded wait for the session lock.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.HookTimeoutMs)*time.Millisecond+hookLockWait(cfg))
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
		handleSessionStart(cfg, in, markerP, lockP)
	case "user-prompt-submit":
		handleUpsertWith(ctx, cfg, client, sessionID, "running", truncate(in.Prompt, 80), "", markerP, lockP, upsertExtra{newTurn: true})
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
		handleNotification(ctx, cfg, client, sessionID, in, markerP, lockP)
	case "stop":
		// A turn ended. Only work that wakes the session with a new turn
		// (subagent, workflow, teammate, cloud session) keeps it running; a
		// background shell, monitor or dev server can run for the whole idle
		// period. session_crons are ignored: a scheduled prompt fires
		// UserPromptSubmit, which marks the session running again.
		if hasWakingBackgroundTasks(in.BackgroundTasks) {
			markBackgroundWake(cfg, markerP, lockP)
			return
		}
		handleUpsert(ctx, cfg, client, sessionID, "done", firstLine(in.LastAssistantMessage), "", markerP, lockP)
	case "stop-failure":
		handleUpsert(ctx, cfg, client, sessionID, "error", stopFailureMessage(in), "", markerP, lockP)
	case "session-end":
		switch in.EndReason {
		case "logout", "prompt_input_exit", "other", "clear", "resume":
			handleDelete(ctx, cfg, sessionID, markerP, lockP)
		}
	}
}

func handleSessionStart(cfg Config, in hookInput, markerP, lockP string) {
	if in.Source == "startup" {
		return
	}
	// A resumed/cleared/compacted session starts over: drop the old marker
	// even if the lock is busy, as SessionEnd does.
	if err := withLockExWait(lockP, hookLockWait(cfg), func() error {
		_ = os.Remove(markerP)
		return nil
	}); err != nil {
		_ = os.Remove(markerP)
	}
}

func handleUpsert(ctx context.Context, cfg Config, client *Client, sessionID, state, message, activity, markerP, lockP string) {
	handleUpsertWith(ctx, cfg, client, sessionID, state, message, activity, markerP, lockP, upsertExtra{})
}

type upsertExtra struct {
	// newTurn (UserPromptSubmit) ends any background-wake hold.
	newTurn             bool
	pending             string
	preToolUseID, preFP string
	skip                func(prev marker) bool
}

func handleUpsertWith(ctx context.Context, cfg Config, client *Client, sessionID, state, message, activity, markerP, lockP string, x upsertExtra) {
	req := cfg.StatusRequest("claude", sessionID, state)
	req.Message = truncate(message, 80)
	req.Activity = truncate(activity, 80)
	cfg.Gauges.Apply(&req)
	var body []byte
	_ = withLockExWait(lockP, hookLockWait(cfg), func() error {
		var ownerPID int
		var ownerStart string
		var track ToolTrack
		changedAt := hookNow().Unix()
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
				if prev.State == state && prev.StateChangedAt != 0 {
					changedAt = prev.StateChangedAt
				}
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
		track.AgentsWait = false
		if x.newTurn || state == "done" || state == "error" {
			track.BackgroundWake = false
		}
		if ownerPID == 0 {
			ownerPID, ownerStart = detectOwner()
		}
		m := marker{StatusRequest: req, OwnerPID: ownerPID, OwnerStart: ownerStart, StateChangedAt: changedAt, ToolTrack: track}
		b, err := json.Marshal(m)
		if err != nil || writeMarker(markerP, b) != nil {
			return nil
		}
		body = b
		return nil
	})
	if body != nil {
		_ = postReconciled(ctx, cfg, client, markerP, lockP, body, hookLockWait(cfg))
	}
}

// markBackgroundWake records on the marker, without a POST (nothing visible
// changes), that Stop kept the session running for waking background work.
func markBackgroundWake(cfg Config, markerP, lockP string) {
	_ = withLockExWait(lockP, hookLockWait(cfg), func() error {
		old, err := readMarker(markerP)
		if err != nil {
			return nil
		}
		var m marker
		if json.Unmarshal(old, &m) != nil || m.BackgroundWake {
			return nil
		}
		m.BackgroundWake = true
		b, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		return writeMarker(markerP, b)
	})
}

// SessionEnd hooks share a 1.5 s budget (plugin hook timeouts don't raise
// it), so the shim, the lock and the DELETE must all fit well inside it.
const (
	sessionEndLockWait   = 200 * time.Millisecond
	sessionEndHTTPBudget = 800 * time.Millisecond
)

func handleDelete(ctx context.Context, cfg Config, sessionID, markerP, lockP string) {
	// Never wait out another holder: the marker goes either way, and the
	// DELETE runs outside the lock so nothing else stalls behind it.
	if err := withLockExWait(lockP, sessionEndLockWait, func() error {
		_ = os.Remove(markerP)
		return nil
	}); err != nil {
		_ = os.Remove(markerP)
	}
	budget := min(time.Duration(cfg.HookTimeoutMs)*time.Millisecond, sessionEndHTTPBudget)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	client := producer.NewClient(cfg.ServerURL, cfg.Token, budget)
	_ = client.Delete(ctx, DeleteRequest{
		Source:  cfg.Source,
		Tool:    "claude",
		Session: sessionID,
	})
}

// hookLockWait bounds how long a hook waits for the session lock. Every
// holder keeps it only for marker file I/O (requests run after release), so
// running out means a wedged holder; the update, marker write included, is
// then dropped and the next hook carries the state.
func hookLockWait(cfg Config) time.Duration {
	return time.Duration(cfg.HookTimeoutMs)*time.Millisecond + 100*time.Millisecond
}

func handleNotification(ctx context.Context, cfg Config, client *Client, sessionID string, in hookInput, markerP, lockP string) {
	msg := in.Message
	switch in.NotificationType {
	case "permission_prompt":
		handleUpsertWith(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP, upsertExtra{
			skip: func(prev marker) bool { return lateResumedPrompt(prev, msg, hookNow()) },
		})
	case "agent_needs_input", "elicitation_dialog", "elicitation_url_dialog", "quota_auto_resume_stale":
		handleUpsert(ctx, cfg, client, sessionID, "waiting", msg, "", markerP, lockP)
	case "elicitation_complete", "elicitation_response":
		// The MCP dialog was answered: end the wait it opened, nothing else.
		handleUpsertWith(ctx, cfg, client, sessionID, "running", msg, "", markerP, lockP, upsertExtra{
			skip: func(prev marker) bool { return prev.State != "waiting" || prev.PendingPermission != "" },
		})
	case "quota_auto_resume_fired":
		handleUpsert(ctx, cfg, client, sessionID, "running", pickFirstNonEmpty(msg, "resumed"), "", markerP, lockP)
	case "quota_auto_resume_disabled":
		handleUpsert(ctx, cfg, client, sessionID, "error", pickFirstNonEmpty(msg, "usage limit"), "", markerP, lockP)
	case "agent_completed":
		handleUpsert(ctx, cfg, client, sessionID, "done", msg, "", markerP, lockP)
	case "idle_prompt":
		// Rescues a session whose Stop was missed; never turns an error into
		// done or replaces the reply line Stop stored.
		handleUpsertWith(ctx, cfg, client, sessionID, "done", msg, "", markerP, lockP, upsertExtra{
			skip: func(prev marker) bool { return prev.State == "done" || prev.State == "error" },
		})
	}
}

// stopFailureLabels names StopFailure's `error` values for a small display.
var stopFailureLabels = map[string]string{
	"rate_limit":             "rate limited",
	"overloaded":             "API overloaded",
	"authentication_failed":  "auth failed",
	"billing_error":          "billing error",
	"server_error":           "API error",
	"max_output_tokens":      "output limit",
	"invalid_request":        "bad request",
	"oauth_org_not_allowed":  "org not allowed",
	"account_on_hold":        "account on hold",
	"model_not_found":        "model not found",
	"cloud_credential_error": "cloud credentials",
}

func stopFailureMessage(in hookInput) string {
	label := stopFailureLabels[in.Error]
	if label == "" && in.Error != "" && in.Error != "unknown" {
		label = strings.ReplaceAll(in.Error, "_", " ")
	}
	details := pickFirstNonEmpty(firstLine(in.ErrorDetails), firstLine(in.LastAssistantMessage))
	switch {
	case label != "" && details != "":
		return label + ": " + details
	case label != "":
		return label
	case details != "":
		return details
	}
	return "error"
}

// hasWakingBackgroundTasks reports a background task that ends by starting a
// new turn. background_tasks is kept raw so an unexpected shape can't fail
// the whole hook decode.
func hasWakingBackgroundTasks(raw json.RawMessage) bool {
	var tasks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &tasks) != nil {
		return false
	}
	for _, t := range tasks {
		switch t.Type {
		case "subagent", "workflow", "teammate", "cloud session":
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
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
