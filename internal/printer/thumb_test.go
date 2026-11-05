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

func bake(t *testing.T, b []byte, kind ThumbKind) image.Image {
	t.Helper()
	out, err := BakeThumb(b, kind)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte{0xFF, 0xC0}) || bytes.Contains(out, []byte{0xFF, 0xC2}) {
		t.Fatalf("%s: not a baseline JPEG", kind)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != ThumbSize || b.Dy() != ThumbSize {
		t.Fatalf("%s: %v, want %d px square", kind, b, ThumbSize)
	}
	return img
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

func TestBakeThumbSolidIsACircleOnBlackWithAFadedRim(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 480, 240))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 0xFA, 0x68, 0x31, 255
	}
	img := bake(t, encodePNG(t, src), ThumbSolid)
	c := ThumbSize / 2
	if got := rgbAt(img, c, c); !near(got, [3]int{0xFA, 0x68, 0x31}, 6) {
		t.Fatalf("centre = %v, want the picture's colour", got)
	}
	for _, p := range [][2]int{{0, 0}, {ThumbSize - 1, 0}, {0, ThumbSize - 1}, {ThumbSize - 1, ThumbSize - 1}, {c, 4}, {c, ThumbSize - 5}} {
		if got := rgbAt(img, p[0], p[1]); !near(got, [3]int{0, 0, 0}, 6) {
			t.Fatalf("%v = %v, want black outside the disc", p, got)
		}
	}
	inner, rim := rgbAt(img, c+160, c), rgbAt(img, c+200, c)
	if inner[0] < 0xE0 || rim[0] > inner[0]/3 {
		t.Fatalf("rim fade: r160 %v, r200 %v", inner, rim)
	}
}

func TestBakeThumbCentresTheModelNotTheTransparentFrame(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	for y := 20; y < 60; y++ {
		for x := 200; x < 240; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 30, G: 200, B: 60, A: 255})
		}
	}
	img := bake(t, encodePNG(t, src), ThumbSolid)
	c := ThumbSize / 2
	if got := rgbAt(img, c, c); !near(got, [3]int{30, 200, 60}, 8) {
		t.Fatalf("centre = %v, want the model, cropped to its opaque box", got)
	}
	if got := rgbAt(img, c-120, c); !near(got, [3]int{30, 200, 60}, 8) {
		t.Fatalf("left of centre = %v, want the model scaled up to the disc", got)
	}
}

func TestBakeThumbGhostIsADimGreyOfTheSolid(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 200, 120, 40, 255
	}
	b := encodePNG(t, src)
	solid, ghost := bake(t, b, ThumbSolid), bake(t, b, ThumbGhost)
	c := ThumbSize / 2
	s, g := rgbAt(solid, c, c), rgbAt(ghost, c, c)
	if !near(g, [3]int{g[1], g[1], g[1]}, 3) {
		t.Fatalf("ghost centre %v is not grey", g)
	}
	luma := 0.2126*float64(s[0]) + 0.7152*float64(s[1]) + 0.0722*float64(s[2])
	if want := int(luma * thumbGhostGain); g[1] < want-4 || g[1] > want+4 {
		t.Fatalf("ghost centre %v, want about %d (25 %% of the solid's luma %v)", g, want, s)
	}
}

func TestBakeThumbRejectsBadInput(t *testing.T) {
	ok := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	if _, err := BakeThumb(ok, "cover"); !errors.Is(err, ErrThumbKind) {
		t.Fatalf("kind cover: %v", err)
	}
	huge := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, MaxThumbSidePx+1, 1)))
	for name, b := range map[string][]byte{"huge": huge, "magic only": []byte("\x89PNG\r\n\x1a\nxx"), "empty": nil} {
		if _, err := BakeThumb(b, ThumbSolid); !errors.Is(err, ErrBadThumbnail) {
			t.Errorf("%s: %v, want ErrBadThumbnail", name, err)
		}
	}
	if _, err := BakeThumb(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 4, 4))), ThumbGhost); err != nil {
		t.Fatalf("fully transparent picture: %v", err)
	}
}
