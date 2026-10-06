package main

import "github.com/tarakanof/ember/internal/producer"

const (
	maxMessageRunes  = 80
	maxActivityRunes = 80
)

func mapThread(th thread) (state, message string, reportable bool) {
	if th.Archived {
		return "", "", false
	}
	if th.PendingKind != "" && th.PendingKind != "auth_refresh" {
		return "waiting", pendingMessage(th.PendingKind), true
	}
	if th.Schema == 1 {
		return mapV1(th)
	}
	status := th.Active
	if status == "" {
		status = th.Status
	}
	switch status {
	// A waiting run is post-turn drain, not the user; see ARCHITECTURE (T3 producer).
	case "preparing", "starting", "running", "waiting":
		return "running", "", true
	case "failed":
		return "error", errorMessage(th.LastError), true
	case "completed":
		if th.HoldsCompletion {
			return "running", "", true
		}
		return "done", "", true
	}
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
	return "", "", false
}

func pendingMessage(kind string) string {
	switch kind {
	case "approval":
		return "approve"
	case "command", "file-read", "file-change", "permission", "mcp-elicitation":
		return "approve " + kind
	default:
		return "needs input"
	}
}

func errorMessage(s string) string {
	if s = producer.Truncate(s, maxMessageRunes); s == "" {
		return "failed"
	}
	return s
}
