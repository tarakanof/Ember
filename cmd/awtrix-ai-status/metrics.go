package main

import (
	"strings"
	"sync"
	"sync/atomic"
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
