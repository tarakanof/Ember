package producer

import (
	"log/slog"
	"sync"
	"time"
)

// FailureLogger throttles repeated slog warnings for the same failure kind, to at most one per period.
type FailureLogger struct {
	period time.Duration
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

// NewFailureLogger returns a FailureLogger that allows at most one Warn per kind every period.
func NewFailureLogger(period time.Duration) *FailureLogger {
	return &FailureLogger{period: period, now: time.Now, last: map[string]time.Time{}}
}

// Warn logs msg tagged with kind unless one for that kind logged within the last period.
func (f *FailureLogger) Warn(logger *slog.Logger, kind, msg string, args ...any) bool {
	f.mu.Lock()
	now := f.now()
	if last, ok := f.last[kind]; ok && now.Sub(last) < f.period {
		f.mu.Unlock()
		return false
	}
	f.last[kind] = now
	f.mu.Unlock()
	logger.Warn(msg, append([]any{"kind", kind}, args...)...)
	return true
}

// Reset clears all throttle state.
func (f *FailureLogger) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = map[string]time.Time{}
}
