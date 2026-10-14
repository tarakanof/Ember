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
	for y := range coverSize / 2 {
		for x := range coverSize {
			if v := img.Pix[img.PixOffset(x, y)]; v > 0 && v < 150 {
				partial++
			}
		}
	}
	if partial < 200 {
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
		case y < 35:
			return color.RGBA{230, 0, 12, 255}
		case y >= 65:
			return color.RGBA{230, 12, 0, 255}
		}
		return color.RGBA{230, 115, 0, 255}
	})
	a := prepared(t, src).Accent
	if q := hexColor(t, a); q[1] > 60 {
		t.Fatalf("accent %s, want the red split across 0 degrees, not the orange", a)
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

func inkOnWhite(x, y int) color.Color {
	dx, dy := float64(x)+0.5-256, float64(y)+0.5-256
	if r := math.Hypot(dx, dy); x%48 < 2 || y%64 < 2 || math.Abs(r-150) < 3 || math.Abs(r-60) < 2 {
		return color.Black
	}
	return color.White
}

func lineArt(x, y int) color.Color {
	if x%20 < 2 {
		return color.Black
	}
	return color.White
}

func rows(lo, hi int, in, out color.Color) func(int, int) color.Color {
	return func(_, y int) color.Color {
		if y >= lo && y < hi {
			return in
		}
		return out
	}
}

func renderedBand(t *testing.T, data []byte) float64 {
	t.Helper()
	out, err := Render(data, Cover, 466)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, out)
	lin := func(v uint32) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	var sum float64
	for y := 280; y < 392; y++ {
		for x := 63; x < 403; x++ {
			r, g, b := level(img, x, y)
			sum += 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
		}
	}
	return sum / (112 * 340)
}

func TestCoverBandLuminanceOfKnownCovers(t *testing.T) {
	grey := func(v uint8) []byte { return testPNG(t, 64, 64, solid(color.RGBA{v, v, v, 255})) }
	for _, c := range []struct {
		name string
		src  []byte
		want float64
	}{
		{"white", testPNG(t, 64, 64, solid(color.White)), 0.5762},
		{"#C8C8C8", grey(0xC8), 0.3352},
		{"yellow #FADC3C", testPNG(t, 64, 64, solid(color.RGBA{0xFA, 0xDC, 0x3C, 255})), 0.4152},
		{"#B0B0B0 just above", grey(0xB0), 0.2529},
		{"#AEAEAE just below", grey(0xAE), 0.2467},
		{"black", testPNG(t, 64, 64, solid(color.Black)), 0},
		{"1 px black and white columns", testPNG(t, 416, 416, func(x, _ int) color.Color { return []color.Color{color.White, color.Black}[x%2] }), 0.2881},
		{"white only in the band", testPNG(t, 416, 416, rows(250, 372, color.White, color.Black)), 0.5762},
		{"white only outside the band", testPNG(t, 416, 416, rows(250, 372, color.Black, color.White)), 0},
	} {
		src, err := decode(c.src)
		if err != nil {
			t.Fatal(err)
		}
		if got := coverBandLuminance(src); math.Abs(got-c.want) > 0.001 {
			t.Errorf("%s: band luminance %.4f, want %.4f", c.name, got, c.want)
		}
		if got, want := prepared(t, c.src).CoverLight, c.want >= 0.25; got != want {
			t.Errorf("%s: cover_light %v, want %v at band %.4f against 0.25", c.name, got, want, c.want)
		}
	}
}

func TestCoverLightMarksLightCovers(t *testing.T) {
	for name, src := range map[string][]byte{
		"white":        testPNG(t, 300, 300, solid(color.White)),
		"#C8C8C8":      testPNG(t, 300, 300, solid(color.RGBA{0xC8, 0xC8, 0xC8, 255})),
		"yellow":       testPNG(t, 300, 300, solid(color.RGBA{0xFA, 0xDC, 0x3C, 255})),
		"10% line art": testPNG(t, 600, 600, lineArt),
		"ink on white": testPNG(t, 512, 512, inkOnWhite),
		"band only":    testPNG(t, 416, 416, rows(250, 372, color.White, color.Black)),
		"1 px stripes": testPNG(t, 416, 416, func(x, _ int) color.Color { return []color.Color{color.White, color.Black}[x%2] }),
	} {
		if !prepared(t, src).CoverLight {
			t.Errorf("%s: cover_light false, rendered band %.3f", name, renderedBand(t, src))
		}
	}
}

func TestCoverLightLeavesDarkBandsOut(t *testing.T) {
	for name, src := range map[string][]byte{
		"dark":               testPNG(t, 300, 300, solid(color.RGBA{30, 25, 60, 255})),
		"photo":              testPNG(t, 300, 300, photoLike(300)),
		"#AEAEAE":            testPNG(t, 300, 300, solid(color.RGBA{0xAE, 0xAE, 0xAE, 255})),
		"light outside band": testPNG(t, 416, 416, rows(250, 372, color.Black, color.White)),
	} {
		if prepared(t, src).CoverLight {
			t.Errorf("%s: cover_light true, rendered band %.3f", name, renderedBand(t, src))
		}
	}
}

func TestCoverBandLuminanceMatchesTheRenderedCover(t *testing.T) {
	for name, data := range map[string][]byte{
		"white":        testPNG(t, 300, 300, solid(color.White)),
		"#C8C8C8":      testPNG(t, 200, 200, solid(color.RGBA{0xC8, 0xC8, 0xC8, 255})),
		"10% line art": testPNG(t, 600, 600, lineArt),
		"ink on white": testPNG(t, 512, 512, inkOnWhite),
		"photo":        testPNG(t, 900, 600, photoLike(900)),
		"band only":    testPNG(t, 416, 416, rows(250, 372, color.White, color.Black)),
	} {
		src, err := decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := coverBandLuminance(src), renderedBand(t, data); math.Abs(got-want) > 0.01 {
			t.Errorf("%s: band luminance %.4f, rendered JPEG %.4f", name, got, want)
		}
	}
}

func BenchmarkCoverBandLuminance(b *testing.B) {
	src, err := decode(photoJPEG(b, 1400))
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		coverBandLuminance(src)
	}
}
