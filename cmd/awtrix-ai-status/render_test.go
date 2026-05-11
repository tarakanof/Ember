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
