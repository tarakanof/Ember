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
		// The tricky case: literal \\n must not become \<space>; the \\ is a
		// single backslash, then \n is a newline-escape → `\` + space.
		// RFC 5545 says \\n is an escaped backslash followed by a literal n
		// (since \\ → \, then n is just the letter n). Verify left-to-right
		// ordering: \\n → `\n` (backslash + letter n, not newline-space).
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
