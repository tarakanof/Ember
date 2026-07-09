package producer

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestFailureLogger_FirstWarnLogsImmediately(t *testing.T) {
	var buf bytes.Buffer
	f := NewFailureLogger(time.Minute)
	logged := f.Warn(newTestLogger(&buf), "post", "status POST failed", "err", "boom")
	if !logged {
		t.Fatal("first Warn should log")
	}
	if !strings.Contains(buf.String(), "status POST failed") || !strings.Contains(buf.String(), "kind=post") {
		t.Errorf("log output missing message/kind: %s", buf.String())
	}
}

func TestFailureLogger_SuppressesRepeatsWithinPeriod(t *testing.T) {
	var buf bytes.Buffer
	f := NewFailureLogger(time.Minute)
	logger := newTestLogger(&buf)
	f.Warn(logger, "post", "status POST failed")
	buf.Reset()
	logged := f.Warn(logger, "post", "status POST failed")
	if logged {
		t.Error("second Warn within period should be suppressed")
	}
	if buf.Len() != 0 {
		t.Errorf("suppressed Warn should not write anything, got: %s", buf.String())
	}
}

func TestFailureLogger_LogsAgainAfterPeriodElapses(t *testing.T) {
	var buf bytes.Buffer
	f := NewFailureLogger(time.Minute)
	logger := newTestLogger(&buf)
	now := time.Now()
	f.now = func() time.Time { return now }
	f.Warn(logger, "post", "status POST failed")
	buf.Reset()
	f.now = func() time.Time { return now.Add(2 * time.Minute) }
	logged := f.Warn(logger, "post", "status POST failed")
	if !logged {
		t.Error("Warn after period elapses should log again")
	}
}

func TestFailureLogger_DifferentKindsThrottledIndependently(t *testing.T) {
	var buf bytes.Buffer
	f := NewFailureLogger(time.Minute)
	logger := newTestLogger(&buf)
	f.Warn(logger, "post", "status POST failed")
	buf.Reset()
	logged := f.Warn(logger, "delete", "status DELETE failed")
	if !logged {
		t.Error("a different kind should not be throttled by another kind's history")
	}
}

func TestFailureLogger_ResetClearsThrottleState(t *testing.T) {
	var buf bytes.Buffer
	f := NewFailureLogger(time.Minute)
	logger := newTestLogger(&buf)
	f.Warn(logger, "post", "status POST failed")
	f.Reset()
	buf.Reset()
	logged := f.Warn(logger, "post", "status POST failed")
	if !logged {
		t.Error("Warn after Reset should log again")
	}
}
