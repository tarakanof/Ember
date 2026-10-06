package nowplaying

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testPNG(t *testing.T, w, h int, fill func(x, y int) color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill(x, y))
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solid(c color.Color) func(int, int) color.Color { return func(int, int) color.Color { return c } }

func isBaseline(b []byte) bool {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == 0xFF {
			switch b[i+1] {
			case 0xC0:
				return true
			case 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
				return false
			}
		}
	}
	return false
}

func TestRenderIsBaselineJPEGOfRequestedSize(t *testing.T) {
	src := testPNG(t, 600, 400, solid(color.RGBA{200, 40, 40, 255}))
	for _, c := range []struct {
		kind Kind
		size int
	}{{Album, 240}, {Artist, 64}, {Backdrop, 466}, {Album, 512}} {
		out, err := Render(src, c.kind, c.size)
		if err != nil {
			t.Fatal(err)
		}
		if !isBaseline(out) {
			t.Fatalf("%s/%d is not baseline JPEG", c.kind, c.size)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != c.size || b.Dy() != c.size {
			t.Fatalf("%s: %dx%d, want %d square", c.kind, b.Dx(), b.Dy(), c.size)
		}
	}
}

func TestRenderCentreCropsAndKeepsColour(t *testing.T) {
	src := testPNG(t, 300, 100, func(x, _ int) color.Color {
		if x < 100 || x >= 200 {
			return color.RGBA{0, 0, 255, 255}
		}
		return color.RGBA{0, 255, 0, 255}
	})
	out, err := Render(src, Album, 32)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(out))
	r, g, b, _ := img.At(16, 16).RGBA()
	if g>>8 < 200 || r>>8 > 40 || b>>8 > 40 {
		t.Fatalf("centre = %d,%d,%d, want green (the centre square)", r>>8, g>>8, b>>8)
	}
}

func TestBackdropIsDimmed(t *testing.T) {
	src := testPNG(t, 100, 100, solid(color.White))
	out, err := Render(src, Backdrop, 64)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(out))
	r, _, _, _ := img.At(32, 32).RGBA()
	if v := r >> 8; v < 80 || v > 100 {
		t.Fatalf("backdrop level = %d, want ~89 (35%%)", v)
	}
}

func TestCheckImageRejectsHugeDimensionsBeforeDecode(t *testing.T) {
	var b bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 5000, 1))
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := CheckImage(b.Bytes()); !errors.Is(err, ErrBadImage) {
		t.Fatalf("err = %v, want ErrBadImage", err)
	}
	if err := CheckImage([]byte("GIF89a")); !errors.Is(err, ErrBadImage) {
		t.Fatal("non-image accepted")
	}
}

func TestCacheEvictsByCountAndBytes(t *testing.T) {
	c := NewCache(2, 10)
	c.Put("a", make([]byte, 4))
	c.Put("b", make([]byte, 4))
	c.Get("a")
	c.Put("c", make([]byte, 4))
	if _, ok := c.Get("b"); ok {
		t.Fatal("least recently used should be evicted by count")
	}
	c.Put("d", make([]byte, 9))
	if n, bytes := c.Len(); n != 1 || bytes != 9 {
		t.Fatalf("len = %d/%d, want 1 entry of 9 bytes", n, bytes)
	}
	c.Put("e", make([]byte, 11))
	if _, ok := c.Get("e"); ok {
		t.Fatal("value above maxBytes stored")
	}
}

func BenchmarkRenderBackdropFrom1400(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1400, 1400))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	for b.Loop() {
		if _, err := Render(buf.Bytes(), Backdrop, 466); err != nil {
			b.Fatal(err)
		}
	}
}

func TestValidateRefusesCorruptBodyBehindValidHeader(t *testing.T) {
	good := testPNG(t, 64, 64, solid(color.White))
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	if err := Validate(good[:len(good)-30]); !errors.Is(err, ErrBadImage) {
		t.Fatalf("err = %v, want ErrBadImage", err)
	}
}

func TestSizesListTheKnobDefaultsFirst(t *testing.T) {
	if Sizes[Album][0] != 240 || Sizes[Artist][0] != 64 || Sizes[Backdrop][0] != 466 {
		t.Fatalf("defaults = %v", Sizes)
	}
}
