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

// drawDigits paints text at (startX, startY) using the 3×5 font.
// Glyph spacing is 1 px so each character advances startX by 4.
// Unsupported runes are silently skipped (callers should pre-validate).
func drawDigits(f *Frame, text string, startX, startY int, c RGB) {
	x := startX
	for _, ch := range text {
		g := glyph(ch)
		if g != nil {
			paintBitmap(f, x, startY, g, c)
		}
		x += 4 // 3-wide glyph + 1-px spacer
	}
}

// robotNormal is the 10-wide × 6-tall mark painted at cols 0–9, rows 1–6.
// Head top, two 1-px eye holes (cols 2 & 7), arms full-width at row 4
// (protruding to cols 0 & 9), body, and four legs at cols 1, 3, 6, 8.
var robotNormal = []string{
	".XXXXXXXX.", // row 1: head top
	".X.XXXX.X.", // row 2: eyes upper (col 2 & col 7 dark)
	".X.XXXX.X.", // row 3: eyes lower
	"XXXXXXXXXX", // row 4: arms
	".XXXXXXXX.", // row 5: body
	".X.X..X.X.", // row 6: legs
}

// robotError differs only in rows 2–4: 3-px tall chevron eye holes
// (> <) sloping inward, plus matching eye-notches in the arms row.
// The arm protrusions at cols 0 and 9 remain lit.
var robotError = []string{
	".XXXXXXXX.",
	".X.XXXX.X.", // row 2: outer holes (col 2 & 7)
	".XX.XX.XX.", // row 3: apex holes (col 3 & 6)
	"XX.XXXX.XX", // row 4: outer holes (col 2 & 7) + arm protrusions retained
	".XXXXXXXX.",
	".X.X..X.X.",
}

// drawRobot paints the robot sprite at cols 0–9, rows 1–6, using c for lit pixels.
// The "error" state selects the chevron-eye sprite; everything else uses normal.
func drawRobot(f *Frame, state string, c RGB) {
	sprite := robotNormal
	if state == "error" {
		sprite = robotError
	}
	paintBitmap(f, 0, 1, sprite, c)
}
