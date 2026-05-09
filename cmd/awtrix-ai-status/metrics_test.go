package main

import (
	"strings"
	"testing"
)

func TestMetrics_NilSafeIncrements(t *testing.T) {
	var m *metrics // explicitly nil — proves bare &App{} can't panic
	m.incRequest("/x", 200)
	m.incPublishOK()
	m.incPublishFail()
	m.incRateLimitDenied()
	m.incSessionEvicted()
	// no asserts — surviving the calls is the assertion
}

func TestMetrics_IncRequest_AggregatesPerKey(t *testing.T) {
	m := newMetrics()
	for i := 0; i < 5; i++ {
		m.incRequest("POST /v1/status", 200)
	}
	for i := 0; i < 3; i++ {
		m.incRequest("POST /v1/status", 400)
	}
	m.incRequest("GET /healthz", 200)

	// Read back via the underlying sync.Map.
	counts := map[requestKey]int64{}
	m.requestsTotal.Range(func(k, v any) bool {
		counts[k.(requestKey)] = loadAtomicInt(v)
		return true
	})
	if got := counts[requestKey{"POST /v1/status", 200}]; got != 5 {
		t.Errorf("POST /v1/status 200 = %d, want 5", got)
	}
	if got := counts[requestKey{"POST /v1/status", 400}]; got != 3 {
		t.Errorf("POST /v1/status 400 = %d, want 3", got)
	}
	if got := counts[requestKey{"GET /healthz", 200}]; got != 1 {
		t.Errorf("GET /healthz 200 = %d, want 1", got)
	}
}

func TestMetrics_IncRequest_UnmatchedFallback(t *testing.T) {
	m := newMetrics()
	m.incRequest("", 404)
	found := false
	m.requestsTotal.Range(func(k, _ any) bool {
		if k.(requestKey).pattern == "<unmatched>" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("empty pattern did not collapse to <unmatched>")
	}
}

func TestPromLabelValue_EscapesPerSpec(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`with "quotes"`, `with \"quotes\"`},
		{`back\slash`, `back\\slash`},
		{"line\nbreak", `line\nbreak`},
		{`mix"\` + "\n", `mix\"\\\n`},
	}
	for _, c := range cases {
		if got := promLabelValue(c.in); got != c.want {
			t.Errorf("promLabelValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// loadAtomicInt extracts the int64 from the *atomic.Int64 stored as the
// sync.Map value. Helper kept here so the test file owns the type assertion.
func loadAtomicInt(v any) int64 {
	type loader interface{ Load() int64 }
	return v.(loader).Load()
}

func TestMetrics_PublishCounters(t *testing.T) {
	m := newMetrics()
	for i := 0; i < 5; i++ {
		m.incPublishOK()
	}
	for i := 0; i < 3; i++ {
		m.incPublishFail()
	}
	if got := m.publishTotalOK.Load(); got != 5 {
		t.Errorf("publishTotalOK = %d, want 5", got)
	}
	if got := m.publishTotalFail.Load(); got != 3 {
		t.Errorf("publishTotalFail = %d, want 3", got)
	}
}

func TestMetrics_RateLimitAndSessionCounters(t *testing.T) {
	m := newMetrics()
	m.incRateLimitDenied()
	m.incRateLimitDenied()
	m.incSessionEvicted()
	if got := m.rateLimitDenied.Load(); got != 2 {
		t.Errorf("rateLimitDenied = %d, want 2", got)
	}
	if got := m.sessionsEvicted.Load(); got != 1 {
		t.Errorf("sessionsEvicted = %d, want 1", got)
	}
}

// Sanity: confirm escaper doesn't %q-escape non-ASCII bytes.
func TestPromLabelValue_NoSurprisingByteEscape(t *testing.T) {
	got := promLabelValue("emoji is fine: \xe2\x98\x83") // ☃
	if strings.Contains(got, `\x`) {
		t.Errorf("promLabelValue should not %%q-escape non-printables: %q", got)
	}
}
