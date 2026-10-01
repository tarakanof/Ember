package producer

import "strings"

// Truncate trims s and, if it's longer than n runes, cuts it to n runes.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
