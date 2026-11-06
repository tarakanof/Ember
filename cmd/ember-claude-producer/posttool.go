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

var exitCodeLine = regexp.MustCompile(`^Exit code (\d{1,3})$`)

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

func annotatedActivity(activity, outcome string) string {
	suffix := " (" + outcome + ")"
	return truncate(activity, 80-len(suffix)) + suffix
}

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

var hookNow = time.Now

const resumeGrace = 15 * time.Second

func endsPendingWait(t ToolTrack, state string, in hookInput) bool {
	if state != "waiting" || t.PendingPermission == "" ||
		t.PendingPermission != permissionFingerprint(in.ToolName, in.ToolInput) {
		return false
	}
	return t.PendingToolUseID == "" || in.ToolUseID == "" || t.PendingToolUseID == in.ToolUseID
}

func lateResumedPrompt(prev marker, msg string, now time.Time) bool {
	if prev.State == "waiting" || prev.ResumedAt == 0 {
		return false
	}
	if now.Sub(time.Unix(prev.ResumedAt, 0)) > resumeGrace {
		return false
	}
	return msg == "" || strings.Contains(msg, prev.ResumedTool)
}

func handleToolOutcome(ctx context.Context, cfg Config, client *Client, in hookInput, outcome, markerP, lockP string) {
	var post []byte
	_ = withLockExWait(lockP, hookLockWait(cfg), func() error {
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
			m.StateChangedAt = hookNow().Unix()
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
		if m.AgentsRun {
			m.AgentsRun = false
			changed = true
		}
		if !changed {
			return nil
		}
		m.HookAt = hookNow().Unix()
		body, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		if writeMarker(markerP, body) == nil && resume {
			post = body
		}
		return nil
	})
	if post != nil {
		_ = postReconciled(ctx, cfg, client, markerP, lockP, post, hookLockWait(cfg))
	}
}
