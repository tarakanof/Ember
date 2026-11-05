package printer

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bake(t *testing.T, b []byte) (Thumbs, image.Image, image.Image) {
	t.Helper()
	th, err := BakeThumbs(b)
	if err != nil {
		t.Fatal(err)
	}
	dec := func(name string, out []byte) image.Image {
		if !bytes.Contains(out, []byte{0xFF, 0xC0}) || bytes.Contains(out, []byte{0xFF, 0xC2}) {
			t.Fatalf("%s: not a baseline JPEG", name)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != ThumbSize || b.Dy() != ThumbSize {
			t.Fatalf("%s: %v, want %d px square", name, b, ThumbSize)
		}
		return img
	}
	return th, dec("solid", th.Solid), dec("ghost", th.Ghost)
}

func rgbAt(img image.Image, x, y int) [3]int {
	r, g, b, _ := img.At(x, y).RGBA()
	return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
}

func near(a, b [3]int, tol int) bool {
	for i := range a {
		if d := a[i] - b[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func filled(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

var black = [3]int{0, 0, 0}

func TestBakeThumbsPutsAWidePictureOnTheHeroFloor(t *testing.T) {
	orange := color.NRGBA{R: 0xFA, G: 0x68, B: 0x31, A: 255}
	th, solid, _ := bake(t, encodePNG(t, filled(480, 240, orange)))
	if th.Top != 160 || th.Bottom != ThumbHeroBottom {
		t.Fatalf("rows = %d..%d, want 160..%d (480x240 scaled by 140/240 into a 280x190 box)", th.Top, th.Bottom, ThumbHeroBottom)
	}
	for _, p := range [][2]int{{233, 165}, {233, 295}, {100, 230}, {366, 230}} {
		if got := rgbAt(solid, p[0], p[1]); !near(got, [3]int{0xFA, 0x68, 0x31}, 8) {
			t.Fatalf("%v = %v, want the picture inside the hero box", p, got)
		}
	}
	for _, p := range [][2]int{{233, 150}, {233, 310}, {233, 400}, {80, 230}, {386, 230}, {0, 0}, {ThumbSize - 1, ThumbSize - 1}} {
		if got := rgbAt(solid, p[0], p[1]); !near(got, black, 8) {
			t.Fatalf("%v = %v, want black outside the hero box", p, got)
		}
	}
}

func TestBakeThumbsKeepsATallModelAboveTheTextBand(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	for y := 0; y < 300; y++ {
		for x := 140; x < 160; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 30, G: 200, B: 60, A: 255})
		}
	}
	th, solid, _ := bake(t, encodePNG(t, src))
	if th.Top != ThumbHeroTop {
		t.Fatalf("top = %d, want %d: a tall model fills the box height", th.Top, ThumbHeroTop)
	}
	if got := rgbAt(solid, 233, 120); !near(got, [3]int{30, 200, 60}, 10) {
		t.Fatalf("model top = %v", got)
	}
	for y := ThumbHeroBottom + 4; y < ThumbSize; y += 8 {
		if got := rgbAt(solid, 233, y); !near(got, black, 8) {
			t.Fatalf("row %d = %v, want the text band black", y, got)
		}
	}
}

func TestBakeThumbsCropsTheTransparentFrame(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	for y := 20; y < 60; y++ {
		for x := 200; x < 240; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 30, G: 200, B: 60, A: 255})
		}
	}
	th, solid, _ := bake(t, encodePNG(t, src))
	if th.Top != ThumbHeroTop {
		t.Fatalf("top = %d, want %d: the 40 px square scales to the box height", th.Top, ThumbHeroTop)
	}
	for _, p := range [][2]int{{233, 205}, {150, 205}, {316, 205}} {
		if got := rgbAt(solid, p[0], p[1]); !near(got, [3]int{30, 200, 60}, 10) {
			t.Fatalf("%v = %v, want the model, cropped to its opaque box and centred", p, got)
		}
	}
	if got := rgbAt(solid, 120, 205); !near(got, black, 8) {
		t.Fatalf("left of the model = %v, want black", got)
	}
}

func TestBakeThumbsGhostIsADimGreyOfTheSolid(t *testing.T) {
	_, solid, ghost := bake(t, encodePNG(t, filled(64, 64, color.NRGBA{R: 200, G: 120, B: 40, A: 255})))
	s, g := rgbAt(solid, 233, 205), rgbAt(ghost, 233, 205)
	if !near(g, [3]int{g[1], g[1], g[1]}, 3) {
		t.Fatalf("ghost %v is not grey", g)
	}
	luma := 0.2126*float64(s[0]) + 0.7152*float64(s[1]) + 0.0722*float64(s[2])
	if want := int(luma * thumbGhostGain); g[1] < want-4 || g[1] > want+4 {
		t.Fatalf("ghost %v, want about %d (25 %% of the solid's luma %v)", g, want, s)
	}
}

func TestBakeThumbRejectsBadInput(t *testing.T) {
	th, err := BakeThumbs(encodePNG(t, opaque(4, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.Kind("cover"); !errors.Is(err, ErrThumbKind) {
		t.Fatalf("kind cover: %v", err)
	}
	huge := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, MaxThumbSidePx+1, 1)))
	for name, b := range map[string][]byte{"huge": huge, "magic only": []byte("\x89PNG\r\n\x1a\nxx"), "empty": nil} {
		if _, err := BakeThumbs(b); !errors.Is(err, ErrBadThumbnail) {
			t.Errorf("%s: %v, want ErrBadThumbnail", name, err)
		}
	}
	clear := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	if _, err := BakeThumbs(clear); !errors.Is(err, ErrBadThumbnail) {
		t.Fatalf("fully transparent picture: %v, want ErrBadThumbnail", err)
	}
	full := encodePNG(t, opaque(64, 64))
	cut := full[:len(full)/2]
	if _, err := png.DecodeConfig(bytes.NewReader(cut)); err != nil {
		t.Fatalf("the cut PNG must keep a valid header: %v", err)
	}
	for name, b := range map[string][]byte{"truncated": cut, "transparent": clear} {
		if err := CheckThumb(b); !errors.Is(err, ErrBadThumbnail) {
			t.Errorf("CheckThumb(%s) = %v, want ErrBadThumbnail", name, err)
		}
	}
	if err := CheckThumb(full); err != nil {
		t.Fatalf("CheckThumb(good) = %v", err)
	}
}

func opaque(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(i), uint8(i>>3), 90, 255
	}
	return img
}
