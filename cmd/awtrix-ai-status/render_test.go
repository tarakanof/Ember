package main

import (
	"testing"
)

func TestFrameAndPainters(t *testing.T) {
	f := &Frame{}

	// paintCell sets a single pixel.
	paintCell(f, 5, 2, RGB{0x12, 0x34, 0x56})
	if !f.Dirty[2][5] {
		t.Fatalf("paintCell(5,2): Dirty[2][5] = false, want true")
	}
	if got := f.Pixels[2][5]; got != (RGB{0x12, 0x34, 0x56}) {
		t.Fatalf("paintCell(5,2) color = %v, want #123456", got)
	}

	// paintRow sets a horizontal run inclusive on both ends.
	paintRow(f, 10, 13, 7, RGB{0xff, 0x00, 0x00})
	for x := 10; x <= 13; x++ {
		if !f.Dirty[7][x] {
			t.Fatalf("paintRow: Dirty[7][%d] = false, want true", x)
		}
	}
	if f.Dirty[7][9] || f.Dirty[7][14] {
		t.Fatalf("paintRow leaked outside [10..13]")
	}

	// paintBitmap paints lit pixels of a sprite at an offset.
	sprite := []string{
		".X.",
		"XXX",
		".X.",
	}
	paintBitmap(f, 20, 0, sprite, RGB{0x00, 0xff, 0x00})
	want := map[[2]int]bool{
		{20, 0}: false, {21, 0}: true, {22, 0}: false,
		{20, 1}: true, {21, 1}: true, {22, 1}: true,
		{20, 2}: false, {21, 2}: true, {22, 2}: false,
	}
	for pos, lit := range want {
		got := f.Dirty[pos[1]][pos[0]]
		if got != lit {
			t.Fatalf("paintBitmap: Dirty[%d][%d] = %v, want %v", pos[1], pos[0], got, lit)
		}
	}

	// Out-of-bounds writes are silent no-ops, not panics.
	paintCell(f, 32, 0, RGB{1, 2, 3})
	paintCell(f, 0, 8, RGB{1, 2, 3})
	paintCell(f, -1, -1, RGB{1, 2, 3})
}

func TestFontLookup(t *testing.T) {
	for _, ch := range "0123456789/+" {
		g := glyph(ch)
		if g == nil {
			t.Fatalf("glyph(%q) = nil, want a sprite", ch)
		}
		if len(g) != 5 {
			t.Fatalf("glyph(%q) height = %d, want 5", ch, len(g))
		}
		for i, row := range g {
			if len(row) != 3 {
				t.Fatalf("glyph(%q) row %d width = %d, want 3", ch, i, len(row))
			}
		}
	}
	if glyph('Z') != nil {
		t.Fatalf("glyph('Z') = non-nil, want nil for unsupported rune")
	}
}

func TestDrawDigits(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		startX    int
		wantLit   map[[2]int]bool
		wantClear [][2]int
	}{
		{
			name:   "single 1 at col 12",
			text:   "1",
			startX: 12,
			wantLit: map[[2]int]bool{
				{13, 1}: true,
				{12, 2}: true, {13, 2}: true,
				{13, 3}: true,
				{13, 4}: true,
				{12, 5}: true, {13, 5}: true, {14, 5}: true,
			},
		},
		{
			name:   "1/3 at col 12",
			text:   "1/3",
			startX: 12,
			wantLit: map[[2]int]bool{
				{13, 1}: true,
				{18, 1}: true, {18, 2}: true, {17, 3}: true, {16, 4}: true,
				{20, 1}: true, {21, 1}: true, {22, 1}: true,
			},
			wantClear: [][2]int{
				{15, 3},
				{19, 3},
				{23, 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &Frame{}
			drawDigits(f, tc.text, tc.startX, 1, RGB{0xff, 0xff, 0xff})
			for pos, want := range tc.wantLit {
				got := f.Dirty[pos[1]][pos[0]]
				if got != want {
					t.Errorf("[%d,%d] lit = %v, want %v", pos[0], pos[1], got, want)
				}
			}
			for _, pos := range tc.wantClear {
				if f.Dirty[pos[1]][pos[0]] {
					t.Errorf("[%d,%d] lit = true, want false (clear)", pos[0], pos[1])
				}
			}
		})
	}
}

func TestDrawRobotNormal(t *testing.T) {
	f := &Frame{}
	drawRobot(f, "running", RGB{0x2e, 0xe8, 0x5e})

	mustLit := [][2]int{
		{1, 1}, {2, 1}, {3, 1}, {7, 1}, {8, 1},
		{0, 4}, {9, 4},
		{1, 6}, {3, 6}, {6, 6}, {8, 6},
	}
	for _, p := range mustLit {
		if !f.Dirty[p[1]][p[0]] {
			t.Errorf("normal: [%d,%d] lit = false, want true", p[0], p[1])
		}
	}

	mustDark := [][2]int{{2, 2}, {2, 3}, {7, 2}, {7, 3}}
	for _, p := range mustDark {
		if f.Dirty[p[1]][p[0]] {
			t.Errorf("normal: [%d,%d] lit = true, want false (eye hole)", p[0], p[1])
		}
	}
}

func TestDrawRobotError(t *testing.T) {
	f := &Frame{}
	drawRobot(f, "error", RGB{0xff, 0x3a, 0x3a})

	holes := [][2]int{{2, 2}, {3, 3}, {2, 4}, {7, 2}, {6, 3}, {7, 4}}
	for _, p := range holes {
		if f.Dirty[p[1]][p[0]] {
			t.Errorf("error: [%d,%d] lit = true, want false (chevron hole)", p[0], p[1])
		}
	}

	for _, x := range []int{0, 9} {
		if !f.Dirty[4][x] {
			t.Errorf("error: arm protrusion at [%d,4] lit = false, want true", x)
		}
	}
}
