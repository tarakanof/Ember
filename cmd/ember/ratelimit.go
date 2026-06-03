package main

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// IPLimiter is a per-IP token-bucket rate limiter. Lives on App so its
// sweeper goroutine has the right lifetime and tests can construct it
// without leaking goroutines. Reads RateLimitConfig via app.cfg.Load()
// once per Allow call so /admin/reload tweaks take effect coherently.
type IPLimiter struct {
	app     *App
	clock   func() time.Time
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

type ipBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
	lastSeen time.Time
}

func NewIPLimiter(app *App) *IPLimiter {
	return &IPLimiter{
		app:     app,
		clock:   time.Now,
		buckets: make(map[string]*ipBucket),
	}
}

// Allow consumes one token from the bucket for the given IP. Returns
// (allowed, retryAfterSeconds). retryAfterSeconds is meaningful only
// when allowed=false.
func (l *IPLimiter) Allow(ip string) (bool, int) {
	rl := l.app.cfg.Load().RateLimit
	if rl.Disabled || rl.Burst <= 0 || rl.RefillPerSec <= 0 {
		return true, 0
	}

	now := l.clock()

	l.mu.Lock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{
			tokens:   float64(rl.Burst),
			lastFill: now,
			lastSeen: now,
		}
		l.buckets[ip] = b
	}
	l.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rl.RefillPerSec
	}
	if b.tokens > float64(rl.Burst) {
		b.tokens = float64(rl.Burst)
	}
	b.lastFill = now
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, retryAfterSeconds(b.tokens, rl.RefillPerSec)
}

// retryAfterSeconds returns the number of whole seconds a client should
// wait before retrying. Computed as ceil((1 - tokens) / refillPerSec),
// clamped to a minimum of 1.
func retryAfterSeconds(tokens, refillPerSec float64) int {
	needed := 1 - tokens
	if needed <= 0 {
		return 1
	}
	secs := math.Ceil(needed / refillPerSec)
	if secs < 1 {
		return 1
	}
	return int(secs)
}

// sweep removes buckets idle longer than IdleEvictSeconds.
func (l *IPLimiter) sweep() {
	rl := l.app.cfg.Load().RateLimit
	if rl.IdleEvictSeconds <= 0 {
		return
	}
	idle := time.Duration(rl.IdleEvictSeconds) * time.Second
	now := l.clock()

	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.buckets {
		b.mu.Lock()
		stale := now.Sub(b.lastSeen) > idle
		b.mu.Unlock()
		if stale {
			delete(l.buckets, ip)
		}
	}
}

// runSweeper runs sweep() every (IdleEvictSeconds / 5) seconds, capped
// between 5s and 60s. Exits on ctx.Done.
func (l *IPLimiter) runSweeper(ctx context.Context) {
	interval := time.Duration(l.app.cfg.Load().RateLimit.IdleEvictSeconds/5) * time.Second
	if interval > 60*time.Second {
		interval = 60 * time.Second
	}
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}

// clientIP returns the host portion of r.RemoteAddr, stripping the port.
// Falls back to RemoteAddr unchanged if it doesn't contain a port.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rateLimit wraps next in a middleware that consults app.limiter. On
// deny, writes 429 + Retry-After header + JSON body, plus an Info
// slog entry.
func rateLimit(app *App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		allowed, retryAfter := app.limiter.Allow(ip)
		if !allowed {
			app.metrics.incRateLimitDenied()
			app.logger.InfoContext(r.Context(), "rate limit exceeded",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"retry_after", retryAfter,
			)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(w, http.StatusTooManyRequests, errors.New("rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
