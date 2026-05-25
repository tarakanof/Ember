package render

// offBG is the colour shown for unlit cells — matches the menu preview's
// "matrix dark" so the LED grid reads as a panel rather than pure black.
var offBG = RGB{0x0d, 0x0d, 0x0d}

// RenderRGBA returns a (32*scale)×(8*scale) 8-bit RGBA buffer for f, scaled
// nearest-neighbour (each matrix pixel becomes a scale×scale block). Lit
// cells use their painted colour; unlit cells use offBG. Alpha is always 255.
// The buffer is row-major, top row first, 4 bytes per pixel — ready for an
// NSBitmapImageRep with bytesPerRow = w*4.
func RenderRGBA(f Frame, scale int) (pix []byte, w, h int) {
	if scale < 1 {
		scale = 1
	}
	w = 32 * scale
	h = 8 * scale
	pix = make([]byte, w*h*4)
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			c := offBG
			if f.Dirty[y][x] {
				c = f.Pixels[y][x]
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					px := x*scale + dx
					py := y*scale + dy
					i := (py*w + px) * 4
					pix[i] = c.R
					pix[i+1] = c.G
					pix[i+2] = c.B
					pix[i+3] = 0xff
				}
			}
		}
	}
	return pix, w, h
}
