package producer

import (
	"strings"
	"unicode/utf8"
)

const (
	trailSeparator = " · "
	trailMaxLen    = 80
)

// PrependTrail returns prev with head prepended as the newest, newest-first trail item, capped at 80 chars by dropping whole trailing items.
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

// AnnotateTrail marks one tool call's trail item with its outcome: the newest item equal to item is replaced by annotated.
func AnnotateTrail(trail, item, annotated string, prepend bool) string {
	item, annotated = strings.TrimSpace(item), strings.TrimSpace(annotated)
	if item == "" || annotated == "" {
		return trail
	}
	if trail != "" {
		items := strings.Split(trail, trailSeparator)
		for i, it := range items {
			if it == item {
				items[i] = annotated
				return capTrail(strings.Join(items, trailSeparator))
			}
		}
	}
	if !prepend {
		return trail
	}
	return PrependTrail(annotated, trail)
}

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
