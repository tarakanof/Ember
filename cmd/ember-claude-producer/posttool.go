package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// Tool-outcome hooks (#76): PostToolUse, PostToolUseFailure, PermissionDenied.
//
// They add no producer state. What they do:
//   - end a "waiting" that a PermissionRequest started, once that same call
//     runs (approved) or is auto-denied: the session goes back to "running"
//     with one POST. Before this, an approved call left the session showing
//     "approve <tool>" until the next hook, which after a turn's last tool
//     call meant until the next prompt.
//   - mark the call's activity-trail item with a short outcome ("exit 1",
//     "failed", "aborted", "denied"). This only rewrites the marker; the next
//     POST carries it (the next hook, or the heartbeat within 10 s), so
//     failures and denials add no requests.
//
// Privacy: tool_response is never decoded, and PostToolUseFailure's error
// text (the failed tool's output) is read only for its "Exit code N" first
// line. Only the fixed outcome words and the exit number leave the machine.

// exitCodeLine matches the first line Claude Code puts on a failed Bash or
// PowerShell call: "Exit code N".
var exitCodeLine = regexp.MustCompile(`^Exit code (\d{1,3})$`)

// failureOutcome is the short, output-free outcome of a PostToolUseFailure.
func failureOutcome(in hookInput) string {
	if in.IsInterrupt {
		return "aborted"
	}
	first, _, _ := strings.Cut(in.Error, "\n")
	if m := exitCodeLine.FindStringSubmatch(strings.TrimSpace(first)); m != nil {
		return "exit " + m[1]
	}
	return "failed"
}

// annotatedActivity appends " (outcome)" to a tool call's activity string,
// shortening the call part so the whole stays within 80 runes.
func annotatedActivity(activity, outcome string) string {
	suffix := " (" + outcome + ")"
	return truncate(activity, 80-len(suffix)) + suffix
}

// permissionFingerprint identifies one tool call across PermissionRequest
// (which has no tool_use_id) and the call's later outcome hook: tool name
// plus tool_input re-encoded canonically (sorted keys), hashed so the marker
// never stores the input itself.
func permissionFingerprint(toolName string, toolInput json.RawMessage) string {
	var v any
	canon := []byte("null")
	if len(toolInput) > 0 && json.Unmarshal(toolInput, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			canon = b
		}
	}
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// hookNow is the clock for ResumedAt; tests replace it.
var hookNow = time.Now

// resumeGrace is how long after a wait ended that a permission_prompt
// Notification naming the same tool is taken as that dialog's late one.
// Claude Code sends it once a prompt has waited about six seconds.
const resumeGrace = 15 * time.Second

// endsPendingWait reports whether outcome hook in belongs to the call the
// session is waiting on: the fingerprint must match, and when both sides
// know the tool_use_id, so must it. The id tells apart an identical call
// retried after an earlier one's outcome was still in flight.
func endsPendingWait(t ToolTrack, state string, in hookInput) bool {
	if state != "waiting" || t.PendingPermission == "" ||
		t.PendingPermission != permissionFingerprint(in.ToolName, in.ToolInput) {
		return false
	}
	return t.PendingToolUseID == "" || in.ToolUseID == "" || t.PendingToolUseID == in.ToolUseID
}

// lateResumedPrompt reports whether a permission_prompt Notification (msg)
// is the late one for a dialog whose call already ran: the session isn't
// waiting, a wait ended within resumeGrace, and msg names that tool (or is
// empty). A new permission dialog always sends PermissionRequest first,
// which clears ResumedAt, so a real new prompt is never dropped by this.
func lateResumedPrompt(prev marker, msg string, now time.Time) bool {
	if prev.State == "waiting" || prev.ResumedAt == 0 {
		return false
	}
	if now.Sub(time.Unix(prev.ResumedAt, 0)) > resumeGrace {
		return false
	}
	return msg == "" || strings.Contains(msg, prev.ResumedTool)
}

// handleToolOutcome applies one outcome hook to an existing marker. outcome
// is "" for a successful call (PostToolUse). No marker, nothing to do: these
// events never create a session.
func handleToolOutcome(ctx context.Context, cfg Config, client *Client, in hookInput, outcome, markerP, lockP string) {
	_ = withLockEx(lockP, func() error {
		old, err := readMarker(markerP)
		if err != nil {
			return nil
		}
		var m marker
		if json.Unmarshal(old, &m) != nil {
			return nil
		}
		resume := endsPendingWait(m.ToolTrack, m.State, in)
		changed := false
		if resume {
			m.State = "running"
			m.Message = truncate(in.ToolName, 80)
			m.PendingPermission, m.PendingToolUseID = "", ""
			m.ResumedTool, m.ResumedAt = in.ToolName, hookNow().Unix()
			changed = true
		}
		if outcome != "" && cfg.ActivityDetailEnabled {
			if act := activityString(in.ToolName, in.ToolInput); act != "" {
				a := producer.AnnotateTrail(m.Activity, act, annotatedActivity(act, outcome), cfg.ActivityTrailEnabled)
				if a != m.Activity {
					m.Activity = a
					changed = true
				}
			}
		}
		if !changed {
			return nil
		}
		body, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		_ = writeMarker(markerP, body)
		if !resume {
			return nil // trail-only: rides the next POST
		}
		_ = client.Post(ctx, wireRequest(cfg, m.StatusRequest))
		return nil
	})
}
