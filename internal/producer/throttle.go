package producer

import (
	"log/slog"
	"sync"
	"time"
)

type FailureLogger struct {
	period time.Duration
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func NewFailureLogger(period time.Duration) *FailureLogger {
	return &FailureLogger{period: period, now: time.Now, last: map[string]time.Time{}}
}

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
