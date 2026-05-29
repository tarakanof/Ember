package render

// Sample values used when a draft toggle is enabled but the live base session
// has no value to show (service unreachable, or the producer wasn't sending
// that field yet). Chosen to look representative on the preview.
const (
	samplePct      = 47
	sampleResetHrs = 3
	sampleActivity = "Bash: npm test"
)

// SampleBaseSession is the placeholder shown when /state is unreachable or
// empty. Lifted from the old menu's state_fetch.go so /v1/preview reproduces
// the previous offline preview.
func SampleBaseSession() Session {
	return Session{Source: "mbp", Tool: "claude", Session: "sample", State: "running"}
}

// ptrInt returns a pointer to v, for Session optional-int fields.
func ptrInt(v int) *int { return &v }
