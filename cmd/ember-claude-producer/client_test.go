package main

import (
	"testing"
	"time"
)

// TestNewClient_UsesHookTimeout confirms the hook-path client stays bound to
// HookTimeoutMs, since it blocks the `claude` CLI and must stay fast.
func TestNewClient_UsesHookTimeout(t *testing.T) {
	cfg := Config{ServerURL: "http://example.invalid", HookTimeoutMs: 500}
	c := NewClient(cfg)
	if got := c.Timeout(); got != 500*time.Millisecond {
		t.Errorf("NewClient timeout = %v, want 500ms", got)
	}
}

// TestNewDaemonClient_IndependentOfHookTimeout is the Task-9 regression test:
// daemon traffic (heartbeat re-POSTs, reap DELETEs, usage POSTs) must not
// inherit the 500ms hook budget, which flaps on slow links with zero evidence.
func TestNewDaemonClient_IndependentOfHookTimeout(t *testing.T) {
	cfg := Config{ServerURL: "http://example.invalid", HookTimeoutMs: 50}
	c := NewDaemonClient(cfg)
	if got := c.Timeout(); got != 5*time.Second {
		t.Errorf("NewDaemonClient timeout = %v, want 5s (independent of HookTimeoutMs=%dms)", got, cfg.HookTimeoutMs)
	}
}
