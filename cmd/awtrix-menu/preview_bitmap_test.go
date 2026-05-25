//go:build darwin

package main

import (
	"testing"

	"github.com/dt/awtrix-ai-status/internal/render"
)

// TestRGBAToImage_MaterializesWithoutCrash guards the NSBitmapImageRep plumbing
// in rgbaToImage. The original bug passed the pixel buffer where AppKit expects
// `unsigned char **` (a pointer to an array of plane pointers), so AppKit
// dereferenced the first pixel's bytes as a pointer and SIGSEGV'd the instant
// anything read the bitmap — which happened every time the preview window
// opened. TIFFRepresentation forces AppKit to read the bitmap, so a malformed
// rep crashes the test process here, while a correct one returns encoded bytes.
func TestRGBAToImage_MaterializesWithoutCrash(t *testing.T) {
	var f render.Frame
	f.Dirty[0][0] = true
	f.Pixels[0][0] = render.RGB{R: 0x2e, G: 0xe8, B: 0x5e}
	f.Dirty[7][31] = true
	f.Pixels[7][31] = render.RGB{R: 0xaa, G: 0x66, B: 0xff}

	pix, w, h := render.RenderRGBA(f, previewScale)
	img := rgbaToImage(pix, w, h)

	if data := img.TIFFRepresentation(); len(data) == 0 {
		t.Fatal("TIFFRepresentation returned no data; bitmap rep is malformed")
	}
}
