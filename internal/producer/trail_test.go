package producer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPrependTrail(t *testing.T) {
	cases := []struct {
		name       string
		head, prev string
		want       string
	}{
		{"empty head keeps prev", "", "Bash: a", "Bash: a"},
		{"empty prev returns head", "Bash: a", "", "Bash: a"},
		{"prepend newest first", "Edit: b", "Bash: a", "Edit: b · Bash: a"},
		{"three items", "Read: c", "Edit: b · Bash: a", "Read: c · Edit: b · Bash: a"},
		{"consecutive dup exact", "Bash: a", "Bash: a", "Bash: a"},
		{"consecutive dup head", "Bash: a", "Bash: a · Edit: b", "Bash: a · Edit: b"},
		{"head trimmed", "  Edit: b  ", "Bash: a", "Edit: b · Bash: a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrependTrail(tc.head, tc.prev); got != tc.want {
				t.Errorf("PrependTrail(%q,%q) = %q, want %q", tc.head, tc.prev, got, tc.want)
			}
		})
	}
}

func TestPrependTrailCapsAt80DroppingWholeItems(t *testing.T) {
	// Distinct heads each iteration (filea..filel) so the trail actually grows
	// and the whole-item-drop path in capTrail is exercised (not short-circuited
	// by the consecutive-dedup guard).
	prev := ""
	for i := 0; i < 12; i++ {
		prev = PrependTrail("Edit: file"+string(rune('a'+i))+".go", prev)
	}
	if len(prev) > 80 {
		t.Fatalf("trail = %d chars (%q), want <= 80", len(prev), prev)
	}
	// Newest item (i=11 → 'l') must be at the head; oldest (i=0 → 'a') dropped.
	if !strings.HasPrefix(prev, "Edit: filel.go") {
		t.Errorf("newest item not at head: %q", prev)
	}
	if strings.Contains(prev, "filea.go") {
		t.Errorf("oldest item should have been dropped: %q", prev)
	}
	if strings.HasSuffix(prev, " ·") || strings.HasSuffix(prev, " · ") {
		t.Errorf("trail ends on a dangling separator: %q", prev)
	}
}

func TestPrependTrailSingleOverlongItemHardCut(t *testing.T) {
	head := "Bash: " + strings.Repeat("y", 200)
	got := PrependTrail(head, "")
	if len(got) != 80 {
		t.Errorf("overlong single item = %d chars, want hard-cut to 80", len(got))
	}
}

// A multibyte overlong item must hard-cut on a rune boundary (never producing
// U+FFFD) and to 80 runes, so the result also stays within the server's 80-rune
// activity limit.
func TestPrependTrailMultibyteHardCutIsRuneSafe(t *testing.T) {
	head := "Bash: " + strings.Repeat("я", 200)
	got := PrependTrail(head, "")
	if n := utf8.RuneCountInString(got); n != 80 {
		t.Errorf("overlong multibyte item = %d runes, want hard-cut to 80", n)
	}
	if !utf8.ValidString(got) {
		t.Errorf("hard-cut produced invalid UTF-8 (mangled rune): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("hard-cut produced U+FFFD replacement char: %q", got)
	}
}

func TestAnnotateTrail(t *testing.T) {
	cases := []struct {
		name, trail, item, annotated string
		prepend                      bool
		want                         string
	}{
		{"head", "Bash: go test · Read: a.go", "Bash: go test", "Bash: go test (exit 1)", true,
			"Bash: go test (exit 1) · Read: a.go"},
		{"later calls came first", "Read: b.go · Bash: go test · Read: a.go", "Bash: go test", "Bash: go test (exit 1)", true,
			"Read: b.go · Bash: go test (exit 1) · Read: a.go"},
		{"newest duplicate wins", "Bash: ls · Edit: x · Bash: ls", "Bash: ls", "Bash: ls (denied)", true,
			"Bash: ls (denied) · Edit: x · Bash: ls"},
		{"no match, trail mode prepends", "Read: a.go", "Bash: go test", "Bash: go test (failed)", true,
			"Bash: go test (failed) · Read: a.go"},
		{"no match, single mode leaves it", "Read: b.go", "Bash: go test", "Bash: go test (failed)", false,
			"Read: b.go"},
		{"single mode match", "Bash: go test", "Bash: go test", "Bash: go test (exit 2)", false,
			"Bash: go test (exit 2)"},
		{"empty trail, trail mode", "", "Bash: x", "Bash: x (denied)", true, "Bash: x (denied)"},
		{"empty item is a no-op", "Read: a", "", "x", true, "Read: a"},
	}
	for _, c := range cases {
		if got := AnnotateTrail(c.trail, c.item, c.annotated, c.prepend); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Annotating can lengthen the trail past 80 runes; whole trailing items are
// dropped, as PrependTrail does.
func TestAnnotateTrailStaysCapped(t *testing.T) {
	head := "Bash: " + strings.Repeat("a", 30)
	trail := PrependTrail(head, "Read: "+strings.Repeat("b", 38))
	got := AnnotateTrail(trail, head, head+" (exit 1)", true)
	if n := utf8.RuneCountInString(got); n > 80 {
		t.Fatalf("annotated trail = %d runes, want <= 80: %q", n, got)
	}
	if got != head+" (exit 1)" {
		t.Errorf("want the overflowing tail item dropped, got %q", got)
	}
}
