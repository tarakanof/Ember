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
	if th.PendingKind != "" {
		return "waiting", pendingMessage(th.PendingKind), true
	}
	if th.Schema == 1 {
		return mapV1(th)
	}
	switch th.Status {
	case "preparing", "queued", "starting", "running":
		return "running", "", true
	case "waiting":
		return "waiting", pendingMessage(""), true
	case "failed":
		return "error", errorMessage(th.LastError), true
	case "completed":
		return "done", "", true
	}
	// idle (never ran), interrupted, cancelled, rolled_back, and unknown values.
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
	case "auth_refresh":
		return "sign in"
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
