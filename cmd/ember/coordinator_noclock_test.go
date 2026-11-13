package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnClockLogsNotConfiguredOncePerMessage(t *testing.T) {
	var buf bytes.Buffer
	c := &coordinator{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	notConfigured := fmt.Errorf("list apps: %w", errClockNotConfigured)

	for i := 0; i < 5; i++ {
		c.warnClock("coord publish failed", notConfigured)
		c.warnClock("device app loop read failed; deferring adopt", notConfigured)
	}
	if n := strings.Count(buf.String(), "coord publish failed"); n != 1 {
		t.Fatalf("publish warnings = %d, want 1", n)
	}
	if n := strings.Count(buf.String(), "deferring adopt"); n != 1 {
		t.Fatalf("adopt warnings = %d, want 1", n)
	}

	real := errors.New("connection refused")
	c.warnClock("coord publish failed", real)
	c.warnClock("coord publish failed", real)
	if n := strings.Count(buf.String(), "connection refused"); n != 2 {
		t.Fatalf("real failures logged %d times, want 2", n)
	}

	c.warnClock("coord publish failed", notConfigured)
	if n := strings.Count(buf.String(), "coord publish failed"); n != 4 {
		t.Fatalf("after a real failure the next not-configured must log again, total %d want 4", n)
	}
}
