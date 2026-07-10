package producer

import "strings"

// Truncate trims s and, if it's longer than n runes, cuts it to n runes.
// Rune-based (not byte-based) so a multibyte character straddling the n-th
// byte doesn't get split into an invalid, mangled trailing byte sequence
// (rendered as U+FFFD by the /state view and the menu).
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
