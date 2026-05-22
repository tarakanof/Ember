package producer

import (
	"strings"
	"testing"
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
