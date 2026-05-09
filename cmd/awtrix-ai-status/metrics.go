package main

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// requestKey is the sync.Map key for awtrix_requests_total. Using a struct
// avoids parsing the key back out of a formatted string.
type requestKey struct {
	pattern string
	status  int
}

// metrics holds counters and the sync-map of per-(pattern,status) request
// counters. Render-time gauges are read from App, not stored here.
//
// All increment helpers are nil-safe: existing tests construct bare
// &App{} literals (see ratelimit_test.go's newTestLimiter), and a panic
// inside a deferred increment would mask the original test failure.
type metrics struct {
	requestsTotal    sync.Map // map[requestKey]*atomic.Int64
	publishTotalOK   atomic.Int64
	publishTotalFail atomic.Int64
	rateLimitDenied  atomic.Int64
	sessionsEvicted  atomic.Int64
}

func newMetrics() *metrics { return &metrics{} }

// incRequest charges one increment to (pattern, status). Empty pattern
// (route didn't match) collapses to a single "<unmatched>" series so a
// 404 spammer can't blow up cardinality.
func (m *metrics) incRequest(pattern string, status int) {
	if m == nil {
		return
	}
	if pattern == "" {
		pattern = "<unmatched>"
	}
	key := requestKey{pattern: pattern, status: status}
	v, _ := m.requestsTotal.LoadOrStore(key, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (m *metrics) incPublishOK() {
	if m != nil {
		m.publishTotalOK.Add(1)
	}
}
func (m *metrics) incPublishFail() {
	if m != nil {
		m.publishTotalFail.Add(1)
	}
}
func (m *metrics) incRateLimitDenied() {
	if m != nil {
		m.rateLimitDenied.Add(1)
	}
}
func (m *metrics) incSessionEvicted() {
	if m != nil {
		m.sessionsEvicted.Add(1)
	}
}

// promLabelValue escapes a string for use as a Prometheus label value per
// the text-exposition spec: backslash → \\, double-quote → \", newline → \n.
// Go's %q would do MORE than this (e.g. escape non-printables as \xNN),
// which Prometheus parsers consider malformed. We escape only what the
// spec requires and pass everything else through.
func promLabelValue(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

// render writes Prometheus exposition format to w. App is consulted for
// gauges that are computed at render time (sessions count, uptime, last
// publish, rate-limit bucket count, build info).
//
// Returns no error: write failures on a Prometheus scrape connection are
// not actionable from inside the render function (the connection is
// already broken; logging here would just spam at scrape rate). The
// caller is the http handler — Go closes the connection cleanly on
// client disconnect.
//
// Order: each metric is preceded by `# HELP` then `# TYPE`. Counters use
// the _total suffix; gauges do not. The format follows
// https://github.com/prometheus/docs/blob/main/content/docs/instrumenting/exposition_formats.md
func (m *metrics) render(w io.Writer, app *App) {
	type entry struct {
		pattern string
		status  int
		n       int64
	}
	var requests []entry
	m.requestsTotal.Range(func(k, v any) bool {
		key := k.(requestKey)
		requests = append(requests, entry{key.pattern, key.status, v.(*atomic.Int64).Load()})
		return true
	})
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].pattern != requests[j].pattern {
			return requests[i].pattern < requests[j].pattern
		}
		return requests[i].status < requests[j].status
	})

	fmt.Fprintln(w, "# HELP awtrix_requests_total HTTP requests by route pattern and status code.")
	fmt.Fprintln(w, "# TYPE awtrix_requests_total counter")
	for _, e := range requests {
		fmt.Fprintf(w, "awtrix_requests_total{pattern=\"%s\",status=\"%d\"} %d\n",
			promLabelValue(e.pattern), e.status, e.n)
	}

	fmt.Fprintln(w, "# HELP awtrix_publish_total AWTRIX publish results.")
	fmt.Fprintln(w, "# TYPE awtrix_publish_total counter")
	fmt.Fprintf(w, "awtrix_publish_total{result=\"ok\"} %d\n", m.publishTotalOK.Load())
	fmt.Fprintf(w, "awtrix_publish_total{result=\"fail\"} %d\n", m.publishTotalFail.Load())

	fmt.Fprintln(w, "# HELP awtrix_rate_limit_denied_total HTTP requests denied by the rate limiter.")
	fmt.Fprintln(w, "# TYPE awtrix_rate_limit_denied_total counter")
	fmt.Fprintf(w, "awtrix_rate_limit_denied_total %d\n", m.rateLimitDenied.Load())

	fmt.Fprintln(w, "# HELP awtrix_sessions_evicted_total Sessions reaped due to staleness or done-TTL.")
	fmt.Fprintln(w, "# TYPE awtrix_sessions_evicted_total counter")
	fmt.Fprintf(w, "awtrix_sessions_evicted_total %d\n", m.sessionsEvicted.Load())

	app.mu.Lock()
	sessionsActive := len(app.sessions)
	var lastPublishUnix int64
	if !app.lastPublishAt.IsZero() {
		lastPublishUnix = app.lastPublishAt.Unix()
	}
	lastPublishOK := 0
	if app.lastPublishOK {
		lastPublishOK = 1
	}
	app.mu.Unlock()

	uptime := time.Since(app.startedAt).Seconds()

	app.limiter.mu.Lock()
	bucketCount := len(app.limiter.buckets)
	app.limiter.mu.Unlock()

	fmt.Fprintln(w, "# HELP awtrix_sessions_active Currently tracked sessions.")
	fmt.Fprintln(w, "# TYPE awtrix_sessions_active gauge")
	fmt.Fprintf(w, "awtrix_sessions_active %d\n", sessionsActive)

	fmt.Fprintln(w, "# HELP awtrix_uptime_seconds Process uptime in seconds.")
	fmt.Fprintln(w, "# TYPE awtrix_uptime_seconds gauge")
	fmt.Fprintf(w, "awtrix_uptime_seconds %.3f\n", uptime)

	fmt.Fprintln(w, "# HELP awtrix_last_publish_unix Unix timestamp of last AWTRIX publish (0 if never).")
	fmt.Fprintln(w, "# TYPE awtrix_last_publish_unix gauge")
	fmt.Fprintf(w, "awtrix_last_publish_unix %d\n", lastPublishUnix)

	fmt.Fprintln(w, "# HELP awtrix_last_publish_ok 1 if the most recent publish succeeded, else 0.")
	fmt.Fprintln(w, "# TYPE awtrix_last_publish_ok gauge")
	fmt.Fprintf(w, "awtrix_last_publish_ok %d\n", lastPublishOK)

	fmt.Fprintln(w, "# HELP awtrix_ratelimit_buckets Active per-IP rate-limit buckets.")
	fmt.Fprintln(w, "# TYPE awtrix_ratelimit_buckets gauge")
	fmt.Fprintf(w, "awtrix_ratelimit_buckets %d\n", bucketCount)

	v := app.versionInfo
	rev := v.Revision
	if rev == "" {
		rev = "unknown" // mirrors runVersion() so /metrics matches the version subcommand
	}
	if v.Dirty {
		rev += "+dirty"
	}
	fmt.Fprintln(w, "# HELP awtrix_build_info Build identity (gauge fixed at 1; identity in labels).")
	fmt.Fprintln(w, "# TYPE awtrix_build_info gauge")
	fmt.Fprintf(w, "awtrix_build_info{revision=\"%s\",go_version=\"%s\"} 1\n",
		promLabelValue(rev), promLabelValue(v.GoVersion))
}

// statusRecorder wraps http.ResponseWriter to capture the first
// WriteHeader call (mirrors net/http's "only the first WriteHeader takes
// effect" contract). Implements Unwrap so http.NewResponseController can
// reach the underlying writer for Flusher/Hijacker if a future handler
// needs them.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

// Write triggers an implicit WriteHeader(200) per Go's contract; capture
// it so handlers that skip an explicit WriteHeader still record 200.
func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// observeRequests is HTTP middleware that captures (matched-pattern, status)
// for every request and increments the awtrix_requests_total counter map.
// It composes inside loggingMiddleware so the access log keeps recording
// the status the inner handler wrote.
//
// /metrics scrapes are deliberately not self-counted — they're regular
// every-15s requests that would dominate the counters without telling us
// anything new.
//
// r.Pattern is set by Go 1.22 ServeMux *before* the inner handler runs, so
// reading it after next.ServeHTTP returns is safe. For unauthenticated
// requests rejected by requireAuth/adminRequireAuth, r.Pattern stays at
// the outer prefix ("/v1/" or "/admin/"); per-route 401 counts are an
// explicit non-feature.
func observeRequests(app *App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/metrics" {
			return
		}
		app.metrics.incRequest(r.Pattern, rec.status)
	})
}
