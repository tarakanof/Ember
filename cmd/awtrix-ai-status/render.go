package main

// RGB is a 24-bit colour. Alpha is implicit (always full).
type RGB struct {
	R, G, B uint8
}

// Frame is a 32×8 pixel buffer. Dirty[y][x] is true when the pixel has
// been painted; otherwise Pixels[y][x] is meaningless and should not be
// emitted. The encoder treats undirty pixels as "off" (black).
type Frame struct {
	Pixels [8][32]RGB
	Dirty  [8][32]bool
}

// paintCell sets a single pixel. Out-of-bounds writes are no-ops so callers
// can paint sprites that overhang the matrix without bounds-checking each one.
func paintCell(f *Frame, x, y int, c RGB) {
	if x < 0 || x >= 32 || y < 0 || y >= 8 {
		return
	}
	f.Pixels[y][x] = c
	f.Dirty[y][x] = true
}

// paintRow paints a horizontal run from x0 to x1 inclusive on row y.
func paintRow(f *Frame, x0, x1, y int, c RGB) {
	if y < 0 || y >= 8 {
		return
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		paintCell(f, x, y, c)
	}
}

// paintBitmap paints the lit pixels of a sprite at (ox, oy). Each row of
// the sprite is a string of 'X' (lit) / '.' (transparent) chars.
func paintBitmap(f *Frame, ox, oy int, sprite []string, c RGB) {
	for y, row := range sprite {
		for x, ch := range row {
			if ch == 'X' {
				paintCell(f, ox+x, oy+y, c)
			}
		}
	}
}

// font3x5 maps a rune to its 3-col × 5-row pixel sprite. Each entry is
// exactly 5 strings of exactly 3 chars; 'X' = lit, '.' = transparent.
// Glyphs are based on the classic Picopixel-style 3×5 family.
var font3x5 = map[rune][]string{
	'0': {"XXX", "X.X", "X.X", "X.X", "XXX"},
	'1': {".X.", "XX.", ".X.", ".X.", "XXX"},
	'2': {"XXX", "..X", "XXX", "X..", "XXX"},
	'3': {"XXX", "..X", "XXX", "..X", "XXX"},
	'4': {"X.X", "X.X", "XXX", "..X", "..X"},
	'5': {"XXX", "X..", "XXX", "..X", "XXX"},
	'6': {"XXX", "X..", "XXX", "X.X", "XXX"},
	'7': {"XXX", "..X", "..X", "..X", "..X"},
	'8': {"XXX", "X.X", "XXX", "X.X", "XXX"},
	'9': {"XXX", "X.X", "XXX", "..X", "XXX"},
	'/': {"..X", "..X", ".X.", "X..", "X.."},
	'+': {"...", ".X.", "XXX", ".X.", "..."},
}

// glyph returns the sprite for the given rune, or nil when unsupported.
// Callers that pass user input must check the return value.
func glyph(r rune) []string {
	g, ok := font3x5[r]
	if !ok {
		return nil
	}
	return g
}
