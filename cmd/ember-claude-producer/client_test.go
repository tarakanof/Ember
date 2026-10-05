package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestNewClient_UsesHookTimeout(t *testing.T) {
	cfg := Config{Common: producer.Common{ServerURL: "http://example.invalid"}, HookTimeoutMs: 500}
	c := NewClient(cfg)
	if got := c.Timeout(); got != 500*time.Millisecond {
		t.Errorf("NewClient timeout = %v, want 500ms", got)
	}
}

func TestNewDaemonClient_IndependentOfHookTimeout(t *testing.T) {
	cfg := Config{Common: producer.Common{ServerURL: "http://example.invalid"}, HookTimeoutMs: 50}
	c := NewDaemonClient(cfg)
	if got := c.Timeout(); got != 5*time.Second {
		t.Errorf("NewDaemonClient timeout = %v, want 5s (independent of HookTimeoutMs=%dms)", got, cfg.HookTimeoutMs)
	}
}
