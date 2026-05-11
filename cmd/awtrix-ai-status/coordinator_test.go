package main

import (
	"context"
	"testing"
	"time"
)

func TestNewCoordinator_DefaultsFromConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	clk := &fakeClock{now: time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)}

	c := newCoordinator(cfg, nil, publisher, clk, nil)

	if c.dwell != 3*time.Second {
		t.Errorf("dwell = %v, want 3s", c.dwell)
	}
	if c.ackTimeout != 30*time.Second {
		t.Errorf("ackTimeout = %v, want 30s", c.ackTimeout)
	}
	if cap(c.cmds) < 1 {
		t.Errorf("cmds channel must be buffered")
	}
	_, cancel := context.WithCancel(context.Background())
	cancel()
}

// fakeClock is already declared in ratelimit_test.go (same package).
