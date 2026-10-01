package render

import "testing"

func TestFontUppercaseComplete(t *testing.T) {
	for r := 'A'; r <= 'Z'; r++ {
		g := glyph(r)
		if g == nil {
			t.Fatalf("font3x5 missing %q", r)
		}
		if len(g) != 5 {
			t.Errorf("%q has %d rows, want 5", r, len(g))
			continue
		}
		for i, row := range g {
			if len(row) != 3 {
				t.Errorf("%q row %d is %d cols, want 3", r, i, len(row))
			}
		}
	}
}
