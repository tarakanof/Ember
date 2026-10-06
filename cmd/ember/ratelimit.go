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

func (l *IPLimiter) Has(ip string) bool {
	rl := l.app.cfg.Load().RateLimit
	if rl.Disabled || rl.Burst <= 0 || rl.RefillPerSec <= 0 {
		return true
	}
	l.mu.Lock()
	b, ok := l.buckets[ip]
	l.mu.Unlock()
	if !ok {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	tokens := b.tokens + l.clock().Sub(b.lastFill).Seconds()*rl.RefillPerSec
	return tokens >= 1
}

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

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

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
