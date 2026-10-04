package main

import "testing"

func TestMapThread(t *testing.T) {
	cases := []struct {
		name       string
		in         thread
		state      string
		message    string
		reportable bool
	}{
		{"v2 running", thread{Schema: 2, Status: "running"}, "running", "", true},
		{"v2 preparing", thread{Schema: 2, Status: "preparing"}, "running", "", true},
		{"v2 queued is not activity", thread{Schema: 2, Status: "queued"}, "", "", false},
		{"v2 active run beats queued presented", thread{Schema: 2, Status: "queued", Active: "running"}, "running", "", true},
		{"v2 active run beats failed presented", thread{Schema: 2, Status: "failed", Active: "starting"}, "running", "", true},
		{"v2 completed holds for background work", thread{Schema: 2, Status: "completed", HoldsCompletion: true}, "running", "", true},
		{"v2 failed ignores background hold", thread{Schema: 2, Status: "failed", HoldsCompletion: true}, "error", "failed", true},
		{"v2 pending request beats hold", thread{Schema: 2, Status: "completed", HoldsCompletion: true, PendingKind: "user_input"}, "waiting", "needs input", true},
		{"v2 starting", thread{Schema: 2, Status: "starting"}, "running", "", true},
		{"v2 waiting is post-turn drain", thread{Schema: 2, Status: "waiting"}, "running", "", true},
		{"v2 auth refresh is not waiting", thread{Schema: 2, Status: "running", PendingKind: "auth_refresh"}, "running", "", true},
		{"v2 pending approval while running", thread{Schema: 2, Status: "running", PendingKind: "command"}, "waiting", "approve command", true},
		{"v2 pending input outlives turn", thread{Schema: 2, Status: "completed", PendingKind: "user_input"}, "waiting", "needs input", true},
		{"v2 pending on idle thread", thread{Schema: 2, Status: "", PendingKind: "permission"}, "waiting", "approve permission", true},
		{"v2 failed", thread{Schema: 2, Status: "failed", LastError: "rate limited"}, "error", "rate limited", true},
		{"v2 failed no message", thread{Schema: 2, Status: "failed"}, "error", "failed", true},
		{"v2 completed", thread{Schema: 2, Status: "completed"}, "done", "", true},
		{"v2 idle", thread{Schema: 2, Status: ""}, "", "", false},
		{"v2 interrupted", thread{Schema: 2, Status: "interrupted"}, "", "", false},
		{"v2 cancelled", thread{Schema: 2, Status: "cancelled"}, "", "", false},
		{"v2 rolled back", thread{Schema: 2, Status: "rolled_back"}, "", "", false},
		{"v2 unknown status", thread{Schema: 2, Status: "teleporting"}, "", "", false},
		{"v2 archived beats running", thread{Schema: 2, Status: "running", Archived: true}, "", "", false},
		{"v2 archived beats pending", thread{Schema: 2, PendingKind: "user_input", Archived: true}, "", "", false},
		{"v1 running", thread{Schema: 1, Status: "running"}, "running", "", true},
		{"v1 starting", thread{Schema: 1, Status: "starting"}, "running", "", true},
		{"v1 ready", thread{Schema: 1, Status: "ready"}, "done", "", true},
		{"v1 error", thread{Schema: 1, Status: "error", LastError: "boom"}, "error", "boom", true},
		{"v1 approval", thread{Schema: 1, Status: "running", PendingKind: "approval"}, "waiting", "approve", true},
		{"v1 user input", thread{Schema: 1, Status: "ready", PendingKind: "user_input"}, "waiting", "needs input", true},
		{"v1 idle", thread{Schema: 1, Status: "idle"}, "", "", false},
		{"v1 stopped", thread{Schema: 1, Status: "stopped"}, "", "", false},
		{"v1 interrupted", thread{Schema: 1, Status: "interrupted"}, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, msg, ok := mapThread(c.in)
			if state != c.state || msg != c.message || ok != c.reportable {
				t.Fatalf("mapThread = (%q, %q, %v), want (%q, %q, %v)", state, msg, ok, c.state, c.message, c.reportable)
			}
		})
	}
}

func TestMapThreadTruncatesLongError(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "error "
	}
	_, msg, _ := mapThread(thread{Schema: 2, Status: "failed", LastError: long})
	if n := len([]rune(msg)); n > maxMessageRunes {
		t.Fatalf("message is %d runes, want <= %d", n, maxMessageRunes)
	}
}
