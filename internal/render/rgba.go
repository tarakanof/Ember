package render

var offBG = RGB{0x0d, 0x0d, 0x0d}

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

func MaskFrameToRegion(f Frame, x0, y0, x1, y1 int) Frame {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > 32 {
		x1 = 32
	}
	if y1 > 8 {
		y1 = 8
	}
	var out Frame
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if f.Dirty[y][x] {
				out.Dirty[y][x] = true
				out.Pixels[y][x] = f.Pixels[y][x]
			}
		}
	}
	return out
}
