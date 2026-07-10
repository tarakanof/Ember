package producer

import (
	"strings"
	"unicode/utf8"
)

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

// capTrail trims s to trailMaxLen runes, preferring to drop whole trailing
// " · "-delimited items; only hard-cuts when the first item alone exceeds the
// limit. Rune-based (like Truncate) so the hard cut never splits a multibyte
// character into a mangled trailing byte (U+FFFD), and so its output stays
// within the server's 80-rune activity limit.
func capTrail(s string) string {
	if utf8.RuneCountInString(s) <= trailMaxLen {
		return s
	}
	items := strings.Split(s, trailSeparator)
	for len(items) > 1 && utf8.RuneCountInString(strings.Join(items, trailSeparator)) > trailMaxLen {
		items = items[:len(items)-1]
	}
	out := strings.Join(items, trailSeparator)
	if utf8.RuneCountInString(out) > trailMaxLen {
		out = string([]rune(out)[:trailMaxLen])
	}
	return out
}
