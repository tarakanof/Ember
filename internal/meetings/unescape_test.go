package meetings

import "testing"

func TestUnescapeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"comma", `a\,b`, "a,b"},
		{"semicolon", `a\;b`, "a;b"},
		{"newline lower", `a\nb`, "a b"},
		{"newline upper", `a\Nb`, "a b"},
		{"backslash", `a\\b`, `a\b`},
		// The tricky case: in the Go raw string `\\n` the two characters are
		// backslash + n — processed left-to-right: \\ → single backslash, then
		// n is just the letter n (not a newline-escape, because the preceding \\
		// already consumed both backslash characters). Result: `\n` (backslash
		// + letter n, not newline-space). This matches RFC 5545 §3.3.11.
		{"backslash-then-n", `\\n`, `\n`},
		{"mixed", `hello\,world\;foo\\bar\nend`, `hello,world;foo\bar end`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := unescapeText(tc.input)
			if got != tc.want {
				t.Errorf("unescapeText(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
