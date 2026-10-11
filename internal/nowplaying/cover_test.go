package nowplaying

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"
)

func photoLike(side int) func(x, y int) color.Color {
	rng := rand.New(rand.NewPCG(1, 2))
	noise := make([]int, side*side)
	for i := range noise {
		noise[i] = rng.IntN(25) - 12
	}
	return func(x, y int) color.Color {
		fx, fy := float64(x)/float64(side), float64(y)/float64(side)
		n := noise[y*side+x]
		ch := func(v float64) uint8 { return uint8(min(max(v+float64(n), 0), 255)) }
		return color.RGBA{
			ch(180*fx + 40*math.Sin(fy*18)),
			ch(90 + 60*math.Sin(fx*7+fy*5)),
			ch(200*(1-fy) + 30*math.Cos(fx*25)),
			255,
		}
	}
}

func decodeJPEG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func level(img image.Image, x, y int) (uint32, uint32, uint32) {
	r, g, b, _ := img.At(x, y).RGBA()
	return r >> 8, g >> 8, b >> 8
}

func luma(img image.Image, x, y int) float64 {
	r, g, b := level(img, x, y)
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}

func TestCoverIsA466BaselineJPEGInsideACircle(t *testing.T) {
	out, err := Render(testPNG(t, 900, 600, photoLike(900)), Cover, 466)
	if err != nil {
		t.Fatal(err)
	}
	if !isBaseline(out) {
		t.Fatal("cover is not baseline JPEG")
	}
	img := decodeJPEG(t, out)
	if b := img.Bounds(); b.Dx() != 466 || b.Dy() != 466 {
		t.Fatalf("cover is %dx%d, want 466 square", b.Dx(), b.Dy())
	}
	for _, p := range []image.Point{{0, 0}, {465, 0}, {0, 465}, {465, 465}, {70, 70}, {395, 395}, {10, 233}, {233, 455}} {
		if l := luma(img, p.X, p.Y); l > 6 {
			t.Errorf("outside the r 208 disc at %v: luma %.0f, want black", p, l)
		}
	}
	for _, p := range []image.Point{{233, 233}, {233, 60}, {100, 233}} {
		if l := luma(img, p.X, p.Y); l < 30 {
			t.Errorf("inside the disc at %v: luma %.0f, want the picture", p, l)
		}
	}
}

func TestCoverEdgeIsAntiAliased(t *testing.T) {
	white := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	img := cover(square(white))
	var partial int
	for y := range coverSize {
		for x := range coverSize {
			if v := img.Pix[img.PixOffset(x, y)]; v > 0 && v < 150 {
				partial++
			}
		}
	}
	if partial < 400 {
		t.Fatalf("%d partial-coverage pixels on the rim, want an anti-aliased ring", partial)
	}
}

func TestCoverBakesTheVignetteAndBottomScrim(t *testing.T) {
	out, err := Render(testPNG(t, 200, 200, solid(color.White)), Cover, 466)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, out)
	centre, rim := luma(img, 233, 233), luma(img, 233-200, 233)
	if centre < 245 {
		t.Fatalf("centre luma %.0f, want untouched white", centre)
	}
	if rim > 0.75*centre || rim < 0.45*centre {
		t.Errorf("rim (r 200) luma %.0f, want ~0.6-0.7 of centre %.0f", rim, centre)
	}
	top, bottom := luma(img, 233, 70), luma(img, 233, 400)
	if top < 245 {
		t.Errorf("upper disc luma %.0f, want no scrim", top)
	}
	if bottom > 0.6*top || bottom < 0.3*top {
		t.Errorf("lower disc (y 400) luma %.0f, want the scrim at ~0.4 of %.0f", bottom, top)
	}
}

func TestCoverSizeStaysBounded(t *testing.T) {
	for _, c := range []struct {
		name string
		src  []byte
		max  int
	}{
		{"photo-like", testPNG(t, 1000, 1000, photoLike(1000)), 60 << 10},
		{"solid", testPNG(t, 300, 300, solid(color.RGBA{30, 90, 200, 255})), 20 << 10},
	} {
		out, err := Render(c.src, Cover, 466)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s cover: %d B", c.name, len(out))
		if len(out) > c.max {
			t.Errorf("%s cover is %d B, want at most %d", c.name, len(out), c.max)
		}
	}
}

func TestCoverRefusesOtherSizes(t *testing.T) {
	if _, err := Render(testPNG(t, 64, 64, solid(color.White)), Cover, 240); err == nil {
		t.Fatal("cover rendered at 240 px")
	}
}

func hexColor(t *testing.T, s string) [3]uint8 {
	t.Helper()
	var q [3]uint8
	if len(s) != 7 || s[0] != '#' {
		t.Fatalf("accent %q is not #RRGGBB", s)
	}
	for i := range 3 {
		var v uint8
		for _, c := range s[1+2*i : 3+2*i] {
			switch {
			case c >= '0' && c <= '9':
				v = v*16 + uint8(c-'0')
			case c >= 'A' && c <= 'F':
				v = v*16 + uint8(c-'A'+10)
			default:
				t.Fatalf("accent %q is not upper-case hex", s)
			}
		}
		q[i] = v
	}
	return q
}

func prepared(t *testing.T, data []byte) *Image {
	t.Helper()
	img, err := Prepare(data)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestAccentReachesThreeToOneOnBlack(t *testing.T) {
	for _, c := range []color.RGBA{{20, 10, 90, 255}, {90, 0, 0, 255}, {0, 50, 20, 255}, {230, 120, 40, 255}} {
		a := prepared(t, testPNG(t, 120, 120, solid(c))).Accent
		if got := contrastOnBlack(hexColor(t, a)); got < accentContrast {
			t.Errorf("source %v: accent %s has %.2f:1 on black, want >= 3", c, a, got)
		}
	}
}

func TestAccentKeepsALegibleColourUnchanged(t *testing.T) {
	if a := prepared(t, testPNG(t, 64, 64, solid(color.RGBA{0xD8, 0x74, 0x3A, 255}))).Accent; a != "#D8743A" {
		t.Fatalf("accent = %s, want the source #D8743A", a)
	}
}

func TestAccentPicksTheDominantSaturatedHue(t *testing.T) {
	src := testPNG(t, 100, 100, func(x, y int) color.Color {
		switch {
		case y < 50:
			return color.RGBA{128, 128, 128, 255}
		case x < 35:
			return color.RGBA{30, 60, 220, 255}
		}
		return color.RGBA{220, 40, 30, 255}
	})
	q := hexColor(t, prepared(t, src).Accent)
	if q[0] < 150 || q[2] > 80 {
		t.Fatalf("accent %v, want the red that covers most of the saturated area", q)
	}
}

func TestAccentFallsBackWithoutSaturatedColour(t *testing.T) {
	grey := testPNG(t, 80, 80, func(x, y int) color.Color { return color.Gray{uint8(x * 3)} })
	if a := prepared(t, grey).Accent; a != FallbackAccent {
		t.Fatalf("accent of a grey picture = %s, want %s", a, FallbackAccent)
	}
	if a := prepared(t, testPNG(t, 40, 40, solid(color.Black))).Accent; a != FallbackAccent {
		t.Fatalf("accent of black = %s, want %s", a, FallbackAccent)
	}
}

func TestAccentIsDeterministic(t *testing.T) {
	src := testPNG(t, 500, 500, photoLike(500))
	first := prepared(t, src).Accent
	for range 3 {
		if a := prepared(t, src).Accent; a != first {
			t.Fatalf("accent %s then %s for the same picture", first, a)
		}
	}
}

func photoJPEG(b testing.TB, side int) []byte {
	b.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	fill := photoLike(side)
	for y := range side {
		for x := range side {
			img.Set(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	return buf.Bytes()
}

func BenchmarkRenderCoverFrom1400(b *testing.B) {
	src := photoJPEG(b, 1400)
	for b.Loop() {
		if _, err := Render(src, Cover, 466); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPrepareFrom1400(b *testing.B) {
	src := photoJPEG(b, 1400)
	for b.Loop() {
		if _, err := Prepare(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPrepareFrom2048(b *testing.B) {
	src := photoJPEG(b, 2048)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Prepare(src); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAccentGridMatchesTheSquareDownsample(t *testing.T) {
	alpha := image.NewNRGBA(image.Rect(3, 5, 93, 45))
	fill := photoLike(100)
	for y := alpha.Rect.Min.Y; y < alpha.Rect.Max.Y; y++ {
		for x := alpha.Rect.Min.X; x < alpha.Rect.Max.X; x++ {
			r, g, b, _ := fill(x, y).RGBA()
			alpha.Set(x, y, color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(x * 2)})
		}
	}
	gray := image.NewGray(image.Rect(0, 0, 17, 60))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i)
	}
	one := image.NewRGBA(image.Rect(0, 0, 1, 1))
	one.Set(0, 0, color.RGBA{200, 30, 30, 255})
	pal := image.NewPaletted(image.Rect(0, 0, 50, 40), color.Palette{color.Black, color.RGBA{10, 200, 90, 255}})
	for i := range pal.Pix {
		pal.Pix[i] = uint8(i % 3 % 2)
	}
	srcs := map[string]image.Image{
		"jpeg 300x200": decodeJPEG(t, encodeJPEG(t, 300, 200)),
		"nrgba alpha":  alpha,
		"gray 17x60":   gray,
		"paletted":     pal,
		"1x1":          one,
	}
	for name, src := range srcs {
		want := resample(square(src), accentGrid)
		got := grid(src, accentGrid)
		for i := range want.pix {
			if got.pix[i] != want.pix[i] {
				t.Fatalf("%s: grid[%d] = %v, want %v from the full square", name, i, got.pix[i], want.pix[i])
			}
		}
	}
}

func encodeJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := photoLike(max(w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func allocated(f func()) uint64 {
	least := ^uint64(0)
	for range 3 {
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		least = min(least, b.TotalAlloc-a.TotalAlloc)
	}
	return least
}

func TestPrepareAllocatesLittleBeyondTheDecode(t *testing.T) {
	src := photoJPEG(t, MaxArtSidePx)
	decode := allocated(func() {
		if _, _, err := image.Decode(bytes.NewReader(src)); err != nil {
			t.Fatal(err)
		}
	})
	prepare := allocated(func() {
		if _, err := Prepare(src); err != nil {
			t.Fatal(err)
		}
	})
	if extra := int64(prepare) - int64(decode); extra > 1<<20 {
		t.Fatalf("Prepare allocates %d B on top of a %d B decode, want at most 1 MB", extra, decode)
	}
}

func TestAccentSkipsNearWhitePastels(t *testing.T) {
	src := testPNG(t, 100, 100, func(x, y int) color.Color {
		if y < 80 {
			return color.RGBA{255, 200, 215, 255}
		}
		return color.RGBA{40, 128, 40, 255}
	})
	q := hexColor(t, prepared(t, src).Accent)
	if q[1] <= q[0] || q[1] <= q[2] {
		t.Fatalf("accent %v, want the green: a near-white pastel must not vote", q)
	}
}

func TestAccentHueBinsWrapAroundRed(t *testing.T) {
	src := testPNG(t, 100, 100, func(x, y int) color.Color {
		switch {
		case y < 30:
			return color.RGBA{230, 0, 12, 255}
		case y < 60:
			return color.RGBA{230, 12, 0, 255}
		}
		return color.RGBA{230, 115, 0, 255}
	})
	q := hexColor(t, prepared(t, src).Accent)
	if q[1] > 60 {
		t.Fatalf("accent %v, want the red split across 0 degrees, not the orange", q)
	}
}

func TestAccentIgnoresTheCornersOutsideTheDisc(t *testing.T) {
	src := testPNG(t, 128, 128, func(x, y int) color.Color {
		dx, dy := float64(x)+0.5-64, float64(y)+0.5-64
		switch {
		case math.Hypot(dx, dy) > 64:
			return color.RGBA{220, 30, 30, 255}
		case math.Abs(dx) < 26 && math.Abs(dy) < 26:
			return color.RGBA{30, 60, 220, 255}
		}
		return color.RGBA{128, 128, 128, 255}
	})
	q := hexColor(t, prepared(t, src).Accent)
	if q[2] <= q[0] {
		t.Fatalf("accent %v, want the blue inside the disc, not the red corners", q)
	}
}
