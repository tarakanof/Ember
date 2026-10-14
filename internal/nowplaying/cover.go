package nowplaying

import (
	"fmt"
	"image"
	"math"
	"sync"
)

const (
	CoverRecipe = "r1"

	coverSize      = 466
	coverDisc      = 416
	coverRadius    = 208.0
	vignetteInner  = 170.0
	vignetteFloor  = 0.6
	scrimFloor     = 0.25
	scrimShare     = 0.35
	accentGrid     = 32
	accentBins     = 24
	accentMinSat   = 0.2
	accentMinValue = 0.15
	accentPaleSat  = 0.3
	accentPaleVal  = 0.95
	accentMinMass  = 4.0
	accentContrast = 3.0
	FallbackAccent = "#F4F4F2"
	bandTop        = 280
	bandBottom     = 392
	bandLeft       = 63
	bandRight      = 403

	coverLightLuminance = 0.22
)

var (
	srgbLinear = func() (t [256]float64) {
		for v := range t {
			c := float64(v) / 255
			if c <= 0.04045 {
				t[v] = c / 12.92
			} else {
				t[v] = math.Pow((c+0.055)/1.055, 2.4)
			}
		}
		return t
	}()
	bandGain = sync.OnceValue(func() []float32 {
		g := make([]float32, 0, (bandBottom-bandTop)*(bandRight-bandLeft))
		for y := bandTop; y < bandBottom; y++ {
			for x := bandLeft; x < bandRight; x++ {
				g = append(g, float32(coverGain(x, y)))
			}
		}
		return g
	})
)

func cover(sq *image.RGBA) *image.RGBA {
	disc := resample(sq, coverDisc)
	out := image.NewRGBA(image.Rect(0, 0, coverSize, coverSize))
	off := (coverSize - coverDisc) / 2
	for y := range coverSize {
		py := min(max(y-off, 0), coverDisc-1)
		for x := range coverSize {
			i := out.PixOffset(x, y)
			out.Pix[i+3] = 255
			k := float32(coverGain(x, y))
			if k == 0 {
				continue
			}
			px := min(max(x-off, 0), coverDisc-1)
			o := (py*coverDisc + px) * 3
			for ch := range 3 {
				out.Pix[i+ch] = uint8(min(max(disc.pix[o+ch]*k+0.5, 0), 255))
			}
		}
	}
	return out
}

func coverGain(x, y int) float64 {
	c := float64(coverSize) / 2
	bottom := c + coverRadius
	scrimTop := bottom - scrimShare*2*coverRadius
	yc := float64(y) + 0.5
	d := math.Hypot(float64(x)+0.5-c, yc-c)
	edge := min(max(coverRadius-d+0.5, 0), 1)
	if edge == 0 {
		return 0
	}
	scrim := 1.0
	if yc > scrimTop {
		t := min((yc-scrimTop)/(bottom-scrimTop), 1)
		scrim = 1 - t*(1-scrimFloor)
	}
	vignette := 1.0
	if d > vignetteInner {
		vignette = 1 - min((d-vignetteInner)/(coverRadius-vignetteInner), 1)*(1-vignetteFloor)
	}
	return edge * vignette * scrim
}

func coverBandLuminance(src image.Image) float64 {
	off := (coverSize - coverDisc) / 2
	disc := gridRows(src, coverDisc, bandTop-off, bandBottom-off)
	gain := bandGain()
	var sum float64
	i := 0
	for y := bandTop; y < bandBottom; y++ {
		row := (y - bandTop) * coverDisc
		for x := bandLeft; x < bandRight; x++ {
			k := gain[i]
			i++
			if k == 0 {
				continue
			}
			o := (row + min(max(x-off, 0), coverDisc-1)) * 3
			var q [3]uint8
			for ch := range 3 {
				q[ch] = uint8(min(max(disc.pix[o+ch]*k+0.5, 0), 255))
			}
			sum += luminance(q)
		}
	}
	return sum / float64(len(gain))
}

func accent(p plane) string {
	var mass [accentBins]float64
	var sum [accentBins][3]float64
	c := float64(p.n) / 2
	for i := 0; i < len(p.pix); i += 3 {
		x, y := i/3%p.n, i/3/p.n
		if math.Hypot(float64(x)+0.5-c, float64(y)+0.5-c) > c {
			continue
		}
		rgb := [3]float64{float64(p.pix[i]) / 255, float64(p.pix[i+1]) / 255, float64(p.pix[i+2]) / 255}
		h, s, v := hsv(rgb)
		if s < accentMinSat || v < accentMinValue || (s < accentPaleSat && v > accentPaleVal) {
			continue
		}
		bin := int(h*accentBins+0.5) % accentBins
		w := s * v
		mass[bin] += w
		for ch := range 3 {
			sum[bin][ch] += w * rgb[ch]
		}
	}
	best := 0
	for i := range mass {
		if mass[i] > mass[best] {
			best = i
		}
	}
	if mass[best] < accentMinMass {
		return FallbackAccent
	}
	var mean [3]float64
	for ch := range 3 {
		mean[ch] = sum[best][ch] / mass[best]
	}
	r, g, b := legible(mean)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func hsv(c [3]float64) (h, s, v float64) {
	hi := max(c[0], c[1], c[2])
	lo := min(c[0], c[1], c[2])
	if hi == 0 {
		return 0, 0, 0
	}
	s, v = (hi-lo)/hi, hi
	d := hi - lo
	if d == 0 {
		return 0, s, v
	}
	switch hi {
	case c[0]:
		h = math.Mod((c[1]-c[2])/d, 6)
	case c[1]:
		h = (c[2]-c[0])/d + 2
	default:
		h = (c[0]-c[1])/d + 4
	}
	h /= 6
	if h < 0 {
		h++
	}
	return h, s, v
}

func legible(c [3]float64) (r, g, b uint8) {
	h, s, l := hsl(c)
	ok := func(l float64) bool { return contrastOnBlack(quantize(fromHSL(h, s, l))) >= accentContrast }
	if !ok(l) {
		lo, hi := l, 1.0
		for range 24 {
			mid := (lo + hi) / 2
			if ok(mid) {
				hi = mid
			} else {
				lo = mid
			}
		}
		l = hi
	}
	q := quantize(fromHSL(h, s, l))
	return q[0], q[1], q[2]
}

func quantize(c [3]float64) [3]uint8 {
	var q [3]uint8
	for ch := range 3 {
		q[ch] = uint8(math.Round(min(max(c[ch], 0), 1) * 255))
	}
	return q
}

func contrastOnBlack(q [3]uint8) float64 {
	return (luminance(q) + 0.05) / 0.05
}

func luminance(q [3]uint8) float64 {
	return 0.2126*srgbLinear[q[0]] + 0.7152*srgbLinear[q[1]] + 0.0722*srgbLinear[q[2]]
}

func hsl(c [3]float64) (h, s, l float64) {
	hi := max(c[0], c[1], c[2])
	lo := min(c[0], c[1], c[2])
	l = (hi + lo) / 2
	if hi == lo {
		return 0, 0, l
	}
	d := hi - lo
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	h, _, _ = hsv(c)
	return h, s, l
}

func fromHSL(h, s, l float64) [3]float64 {
	if s == 0 {
		return [3]float64{l, l, l}
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	ch := func(t float64) float64 {
		t = t - math.Floor(t)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return [3]float64{ch(h + 1.0/3), ch(h), ch(h - 1.0/3)}
}
