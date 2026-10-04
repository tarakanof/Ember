package main

import "github.com/tarakanof/ember/internal/producer"

const (
	maxMessageRunes  = 80
	maxActivityRunes = 80
)

// mapThread turns a T3 thread into an Ember state. reportable is false when
// the thread should not be on the clock (idle, archived, interrupted by the
// user, or a status this producer does not know).
func mapThread(th thread) (state, message string, reportable bool) {
	if th.Archived {
		return "", "", false
	}
	// auth_refresh is T3-internal: its UI never asks the user about it.
	if th.PendingKind != "" && th.PendingKind != "auth_refresh" {
		return "waiting", pendingMessage(th.PendingKind), true
	}
	if th.Schema == 1 {
		return mapV1(th)
	}
	// T3's activityRunStatus ?? status: an active run anywhere in the thread
	// wins over the presented run's status.
	status := th.Active
	if status == "" {
		status = th.Status
	}
	switch status {
	// A run "waiting" is post-turn drain (checkpoint capture, background
	// work), not the user: T3's own awareness shows it as running. Requests
	// that do block on the user are runtime requests (PendingKind).
	case "preparing", "starting", "running", "waiting":
		return "running", "", true
	case "failed":
		return "error", errorMessage(th.LastError), true
	case "completed":
		// Work that will wake the agent keeps the run going.
		if th.HoldsCompletion {
			return "running", "", true
		}
		return "done", "", true
	}
	// idle (never ran), queued (awareness maps it to null), interrupted,
	// cancelled, rolled_back, and unknown values.
	return "", "", false
}

func mapV1(th thread) (string, string, bool) {
	switch th.Status {
	case "starting", "running":
		return "running", "", true
	case "ready":
		return "done", "", true
	case "error":
		return "error", errorMessage(th.LastError), true
	}
	// idle, stopped, interrupted, unknown.
	return "", "", false
}

func pendingMessage(kind string) string {
	switch kind {
	case "approval":
		return "approve"
	case "command", "file-read", "file-change", "permission", "mcp-elicitation":
		return "approve " + kind
	default: // user_input, dynamic_tool_call, unknown
		return "needs input"
	}
}

func errorMessage(s string) string {
	if s = producer.Truncate(s, maxMessageRunes); s == "" {
		return "failed"
	}
	return s
}
