package producer

import "testing"

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than n untouched", "hello", 80, "hello"},
		{"exactly n untouched", "hello", 5, "hello"},
		{"trims surrounding whitespace first", "  hello  ", 80, "hello"},
		{"ascii cut at byte boundary", "abcdefghij", 5, "abcde"},
		// U+1F600 (grinning face emoji) is 4 bytes in UTF-8. A byte-based
		// slice at n=5 would cut mid-rune, producing an invalid tail that
		// decodes to U+FFFD in the /state view and the menu.
		{"emoji at the exact truncation boundary", "abcd\U0001F600efgh", 5, "abcd\U0001F600"},
		{"emoji just past the boundary is dropped whole", "abcd\U0001F600efgh", 4, "abcd"},
		{"multibyte (non-emoji) rune at boundary", "café résumé", 4, "café"},
		{"all multibyte runes", "日本語テスト", 3, "日本語"},
		{"n is zero", "hello", 0, ""},
		{"empty string", "", 10, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(c.in, c.n)
			if got != c.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}
