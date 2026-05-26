package render

import "testing"

func TestRenderRGBADimensions(t *testing.T) {
	var f Frame
	pix, w, h := RenderRGBA(f, 4)
	if w != 32*4 || h != 8*4 {
		t.Fatalf("dims = %dx%d, want %dx%d", w, h, 32*4, 8*4)
	}
	if len(pix) != w*h*4 {
		t.Fatalf("len(pix) = %d, want %d", len(pix), w*h*4)
	}
}

func TestRenderRGBALitAndOffPixels(t *testing.T) {
	var f Frame
	// Light one pixel at (x=1,y=0) red; paintCell sets Pixels[y][x] + Dirty.
	paintCell(&f, 1, 0, RGB{0xff, 0x00, 0x00})
	scale := 3
	pix, w, _ := RenderRGBA(f, scale)
	at := func(x, y int) (r, g, b, a byte) {
		i := (y*w + x) * 4
		return pix[i], pix[i+1], pix[i+2], pix[i+3]
	}
	// Inside the lit block (x in [3,5], y in [0,2]) must be pure red, opaque.
	r, g, b, a := at(3, 0)
	if r != 0xff || g != 0 || b != 0 || a != 0xff {
		t.Fatalf("lit pixel = %d,%d,%d,%d want 255,0,0,255", r, g, b, a)
	}
	// An off pixel (x=0 block) must be the matrix-dark background 0x0d.
	r, g, b, a = at(0, 0)
	if r != 0x0d || g != 0x0d || b != 0x0d || a != 0xff {
		t.Fatalf("off pixel = %d,%d,%d,%d want 13,13,13,255", r, g, b, a)
	}
}

func TestRenderRGBAClampsScaleBelowOne(t *testing.T) {
	var f Frame
	pix, w, h := RenderRGBA(f, 0)
	if w != 32 || h != 8 {
		t.Fatalf("scale 0 should clamp to 1: dims = %dx%d, want 32x8", w, h)
	}
	if len(pix) != 32*8*4 {
		t.Fatalf("len(pix) = %d, want %d", len(pix), 32*8*4)
	}
}

func TestMaskFrameToRegionKeepsOnlyRegion(t *testing.T) {
	var f Frame
	paintCell(&f, 26, 2, RGB{0x00, 0xff, 0x00}) // inside cols [25,31)×rows [1,6)
	paintCell(&f, 5, 0, RGB{0xff, 0x00, 0x00})  // robot area, must be cleared

	m := MaskFrameToRegion(f, 25, 1, 31, 6)

	// The in-region pixel keeps its colour and stays at its true position.
	if !m.Dirty[2][26] || m.Pixels[2][26] != (RGB{0x00, 0xff, 0x00}) {
		t.Errorf("in-region cell (26,2) = dirty:%v %v, want lit green", m.Dirty[2][26], m.Pixels[2][26])
	}
	// The robot pixel outside the region is cleared.
	if m.Dirty[0][5] {
		t.Errorf("out-of-region cell (5,0) should be cleared, but is lit")
	}
	// Full 32×8 extent is preserved (renders as a dark field at full size).
	if pix, w, h := RenderRGBA(m, 2); w != 64 || h != 16 || len(pix) != 64*16*4 {
		t.Errorf("masked frame renders %dx%d (len %d), want 64x16 full display", w, h, len(pix))
	}
}

func TestMaskFrameToRegionClampsBounds(t *testing.T) {
	var f Frame
	paintCell(&f, 31, 7, RGB{0x00, 0x00, 0xff})
	// Over-wide bounds must clamp, not panic, and keep the corner pixel.
	m := MaskFrameToRegion(f, -4, -4, 99, 99)
	if !m.Dirty[7][31] {
		t.Error("clamped full-frame mask should keep corner pixel (31,7)")
	}
}
