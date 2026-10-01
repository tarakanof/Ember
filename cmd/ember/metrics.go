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

type requestKey struct {
	pattern string
	status  int
}

type metrics struct {
	requestsTotal    sync.Map
	publishTotalOK   atomic.Int64
	publishTotalFail atomic.Int64
	publishRetries   atomic.Int64
	rateLimitDenied  atomic.Int64
	sessionsEvicted  atomic.Int64
	commandsDropped  atomic.Int64
}

func newMetrics() *metrics { return &metrics{} }

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
func (m *metrics) incPublishRetry() {
	if m != nil {
		m.publishRetries.Add(1)
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
func (m *metrics) incCommandDropped() {
	if m != nil {
		m.commandsDropped.Add(1)
	}
}

func promLabelValue(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

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

	fmt.Fprintln(w, "# HELP ember_requests_total HTTP requests by route pattern and status code.")
	fmt.Fprintln(w, "# TYPE ember_requests_total counter")
	for _, e := range requests {
		fmt.Fprintf(w, "ember_requests_total{pattern=\"%s\",status=\"%d\"} %d\n",
			promLabelValue(e.pattern), e.status, e.n)
	}

	fmt.Fprintln(w, "# HELP ember_publish_total AWTRIX publish results.")
	fmt.Fprintln(w, "# TYPE ember_publish_total counter")
	fmt.Fprintf(w, "ember_publish_total{result=\"ok\"} %d\n", m.publishTotalOK.Load())
	fmt.Fprintf(w, "ember_publish_total{result=\"fail\"} %d\n", m.publishTotalFail.Load())

	fmt.Fprintln(w, "# HELP ember_publish_retries_total Device calls (pushed apps, display-hold settings and switches) retried after a lost attempt.")
	fmt.Fprintln(w, "# TYPE ember_publish_retries_total counter")
	fmt.Fprintf(w, "ember_publish_retries_total %d\n", m.publishRetries.Load())

	fmt.Fprintln(w, "# HELP ember_rate_limit_denied_total HTTP requests denied by the rate limiter.")
	fmt.Fprintln(w, "# TYPE ember_rate_limit_denied_total counter")
	fmt.Fprintf(w, "ember_rate_limit_denied_total %d\n", m.rateLimitDenied.Load())

	fmt.Fprintln(w, "# HELP ember_sessions_evicted_total Sessions reaped due to staleness or done-TTL.")
	fmt.Fprintln(w, "# TYPE ember_sessions_evicted_total counter")
	fmt.Fprintf(w, "ember_sessions_evicted_total %d\n", m.sessionsEvicted.Load())

	fmt.Fprintln(w, "# HELP ember_coordinator_commands_dropped_total State-change commands dropped because the coordinator command buffer was full.")
	fmt.Fprintln(w, "# TYPE ember_coordinator_commands_dropped_total counter")
	fmt.Fprintf(w, "ember_coordinator_commands_dropped_total %d\n", m.commandsDropped.Load())

	sessionsActive := len(app.sessions.View().Sessions)
	app.mu.Lock()
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

	fmt.Fprintln(w, "# HELP ember_sessions_active Currently tracked sessions.")
	fmt.Fprintln(w, "# TYPE ember_sessions_active gauge")
	fmt.Fprintf(w, "ember_sessions_active %d\n", sessionsActive)

	fmt.Fprintln(w, "# HELP ember_uptime_seconds Process uptime in seconds.")
	fmt.Fprintln(w, "# TYPE ember_uptime_seconds gauge")
	fmt.Fprintf(w, "ember_uptime_seconds %.3f\n", uptime)

	fmt.Fprintln(w, "# HELP ember_last_publish_unix Unix timestamp of last AWTRIX publish (0 if never).")
	fmt.Fprintln(w, "# TYPE ember_last_publish_unix gauge")
	fmt.Fprintf(w, "ember_last_publish_unix %d\n", lastPublishUnix)

	fmt.Fprintln(w, "# HELP ember_last_publish_ok 1 if the most recent publish succeeded, else 0.")
	fmt.Fprintln(w, "# TYPE ember_last_publish_ok gauge")
	fmt.Fprintf(w, "ember_last_publish_ok %d\n", lastPublishOK)

	fmt.Fprintln(w, "# HELP ember_ratelimit_buckets Active per-IP rate-limit buckets.")
	fmt.Fprintln(w, "# TYPE ember_ratelimit_buckets gauge")
	fmt.Fprintf(w, "ember_ratelimit_buckets %d\n", bucketCount)

	v := app.versionInfo
	rev := v.Revision
	if rev == "" {
		rev = "unknown"
	}
	if v.Dirty {
		rev += "+dirty"
	}
	fmt.Fprintln(w, "# HELP ember_build_info Build identity (gauge fixed at 1; identity in labels).")
	fmt.Fprintln(w, "# TYPE ember_build_info gauge")
	fmt.Fprintf(w, "ember_build_info{revision=\"%s\",go_version=\"%s\",version=\"%s\"} 1\n",
		promLabelValue(rev), promLabelValue(v.GoVersion), promLabelValue(v.Version))
}

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

// Write triggers an implicit WriteHeader(200) per Go's contract; capture it so handlers that skip an explicit WriteHeader still record 200.
func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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
