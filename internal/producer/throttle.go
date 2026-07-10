package producer

import (
	"log/slog"
	"sync"
	"time"
)

// FailureLogger throttles repeated slog warnings for the same failure kind,
// so a persistently unreachable server doesn't flood the log every tick while
// still surfacing the first occurrence (and later ones, once the throttle
// period has elapsed) — the daemon loops previously discarded every network
// error with `_ = client.Post(...)`, giving zero visibility into a stalled
// link.
type FailureLogger struct {
	period time.Duration
	now    func() time.Time

	mu   sync.Mutex // protects last
	last map[string]time.Time
}

// NewFailureLogger returns a FailureLogger that allows at most one Warn per
// kind every period.
func NewFailureLogger(period time.Duration) *FailureLogger {
	return &FailureLogger{period: period, now: time.Now, last: map[string]time.Time{}}
}

// Warn logs msg at Warn through logger, tagged with kind, unless a Warn for
// the same kind already logged within the last period — in which case it is
// suppressed. Returns whether it actually logged. Callers must ensure msg and
// args never carry a token or a credentialed URL.
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

// Reset clears all throttle state, so the next Warn for any kind logs
// immediately regardless of recent history.
func (f *FailureLogger) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = map[string]time.Time{}
}
