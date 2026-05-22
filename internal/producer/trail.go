package producer

import "strings"

const (
	trailSeparator = " · "
	trailMaxLen    = 80
)

// PrependTrail returns prev with head prepended as the newest, newest-first
// trail item, joined by " · " and capped at 80 chars by dropping whole
// trailing items. Empty head returns prev unchanged. A head equal to the
// current newest item is collapsed (no stutter on repeated actions).
func PrependTrail(head, prev string) string {
	head = strings.TrimSpace(head)
	if head == "" {
		return prev
	}
	if prev == "" {
		return capTrail(head)
	}
	if prev == head || strings.HasPrefix(prev, head+trailSeparator) {
		return prev
	}
	return capTrail(head + trailSeparator + prev)
}

// capTrail trims s to trailMaxLen, preferring to drop whole trailing
// " · "-delimited items; only hard-cuts when the first item alone exceeds the
// limit.
func capTrail(s string) string {
	if len(s) <= trailMaxLen {
		return s
	}
	items := strings.Split(s, trailSeparator)
	for len(items) > 1 && len(strings.Join(items, trailSeparator)) > trailMaxLen {
		items = items[:len(items)-1]
	}
	out := strings.Join(items, trailSeparator)
	if len(out) > trailMaxLen {
		out = out[:trailMaxLen]
	}
	return out
}
