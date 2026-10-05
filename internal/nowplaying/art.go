package nowplaying

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // PNG artwork from Music.app and Deezer
	"sync"
)

// Art limits: source bytes, source dimensions, and the sizes served.
const (
	MaxArtBytes   = 2 << 20
	MaxArtSidePx  = 4096
	MinSizePx     = 16
	MaxSizePx     = 512
	jpegQuality   = 80
	backdropDim   = 0.35
	backdropPass  = 3
	backdropRatio = 40 // blur radius = size / backdropRatio
)

// Default rendered sizes per kind, matching the knob's layout.
var DefaultSize = map[Kind]int{Album: 240, Artist: 64, Backdrop: 466}

// ErrBadImage is a source picture that is not a JPEG/PNG within the limits.
var ErrBadImage = errors.New("artwork must be a JPEG or PNG of at most 2 MB and 4096x4096 px")

// CheckImage validates source bytes without decoding the pixels.
func CheckImage(data []byte) error {
	if len(data) == 0 || len(data) > MaxArtBytes {
		return ErrBadImage
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return ErrBadImage
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxArtSidePx || cfg.Height > MaxArtSidePx {
		return ErrBadImage
	}
	return nil
}

// Render turns a source picture into a square baseline JPEG of size px:
// centre-cropped, resampled, and for Backdrop blurred and dimmed. Alpha is
// flattened onto black.
func Render(data []byte, kind Kind, size int) ([]byte, error) {
	if err := CheckImage(data); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadImage, err)
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side)
	sq := image.NewRGBA(crop)
	draw.Draw(sq, crop, image.NewUniform(color.Black), image.Point{}, draw.Src)
	off := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	draw.Draw(sq, crop, src, off, draw.Over)
	px := resample(sq, size)
	if kind == Backdrop {
		px.blur(max(1, size/backdropRatio), backdropPass)
		px.scale(backdropDim)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, px.rgba(), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode artwork: %w", err)
	}
	return out.Bytes(), nil
}

// plane is an RGB float image, n×n.
type plane struct {
	n   int
	pix []float32 // r,g,b per pixel
}

// resample scales a square RGBA to n×n by area averaging (each output
// pixel is the coverage-weighted mean of the source pixels under it), which
// also degrades to a two-pixel blend when enlarging.
func resample(src *image.RGBA, n int) plane {
	s := src.Bounds().Dx()
	tmp := make([]float32, n*s*3) // n wide, s tall
	wx := weights(s, n)
	for y := 0; y < s; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < n; x++ {
			var r, g, b float32
			for _, w := range wx[x] {
				p := row[w.i*4:]
				r += w.w * float32(p[0])
				g += w.w * float32(p[1])
				b += w.w * float32(p[2])
			}
			o := (y*n + x) * 3
			tmp[o], tmp[o+1], tmp[o+2] = r, g, b
		}
	}
	out := plane{n: n, pix: make([]float32, n*n*3)}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b float32
			for _, w := range wx[y] {
				o := (w.i*n + x) * 3
				r += w.w * tmp[o]
				g += w.w * tmp[o+1]
				b += w.w * tmp[o+2]
			}
			o := (y*n + x) * 3
			out.pix[o], out.pix[o+1], out.pix[o+2] = r, g, b
		}
	}
	return out
}

type tap struct {
	i int
	w float32
}

// weights lists, for each of n outputs, the source pixels it covers out of
// s and their normalised overlap.
func weights(s, n int) [][]tap {
	out := make([][]tap, n)
	scale := float64(s) / float64(n)
	for i := range out {
		lo, hi := float64(i)*scale, float64(i+1)*scale
		if scale < 1 {
			c := (float64(i)+0.5)*scale - 0.5
			lo, hi = c, c+1
		}
		var taps []tap
		var sum float64
		for j := int(lo); float64(j) < hi; j++ {
			ov := min(hi, float64(j+1)) - max(lo, float64(j))
			if ov <= 0 {
				continue
			}
			jj := min(max(j, 0), s-1)
			taps = append(taps, tap{jj, float32(ov)})
			sum += ov
		}
		for k := range taps {
			taps[k].w /= float32(sum)
		}
		out[i] = taps
	}
	return out
}

// blur applies passes of a separable box blur of radius r (three passes
// approximate a Gaussian), clamping at the edges.
func (p plane) blur(r, passes int) {
	n := p.n
	line := make([]float32, n*3)
	run := func(get func(i int) int) {
		for c := 0; c < 3; c++ {
			var acc float32
			for k := -r; k <= r; k++ {
				acc += p.pix[get(min(max(k, 0), n-1))+c]
			}
			for i := 0; i < n; i++ {
				line[i*3+c] = acc / float32(2*r+1)
				acc += p.pix[get(min(i+r+1, n-1))+c] - p.pix[get(max(i-r, 0))+c]
			}
		}
		for i := 0; i < n; i++ {
			o := get(i)
			copy(p.pix[o:o+3], line[i*3:i*3+3])
		}
	}
	for range passes {
		for y := 0; y < n; y++ {
			run(func(i int) int { return (y*n + i) * 3 })
		}
		for x := 0; x < n; x++ {
			run(func(i int) int { return (i*n + x) * 3 })
		}
	}
}

func (p plane) scale(k float32) {
	for i := range p.pix {
		p.pix[i] *= k
	}
}

func (p plane) rgba() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, p.n, p.n))
	for i := 0; i < p.n*p.n; i++ {
		for c := 0; c < 3; c++ {
			img.Pix[i*4+c] = uint8(min(max(p.pix[i*3+c]+0.5, 0), 255))
		}
		img.Pix[i*4+3] = 255
	}
	return img
}

// Cache holds rendered pictures in RAM, bounded by entry count and bytes,
// least recently used first out. Safe for concurrent use.
type Cache struct {
	mu         sync.Mutex // protects ll, items, bytes
	maxEntries int
	maxBytes   int
	bytes      int
	ll         *list.List
	items      map[string]*list.Element
}

type cacheItem struct {
	key  string
	data []byte
}

// NewCache returns a cache bounded at maxEntries and maxBytes.
func NewCache(maxEntries, maxBytes int) *Cache {
	return &Cache{maxEntries: maxEntries, maxBytes: maxBytes, ll: list.New(), items: make(map[string]*list.Element)}
}

// Get returns a cached value and marks it recently used.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cacheItem).data, true
}

// Put stores a value, evicting the least recently used past either bound.
// A value larger than maxBytes is not stored.
func (c *Cache) Put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) > c.maxBytes {
		return
	}
	if el, ok := c.items[key]; ok {
		c.bytes -= len(el.Value.(*cacheItem).data)
		c.ll.Remove(el)
		delete(c.items, key)
	}
	c.items[key] = c.ll.PushFront(&cacheItem{key, data})
	c.bytes += len(data)
	for c.ll.Len() > c.maxEntries || c.bytes > c.maxBytes {
		el := c.ll.Back()
		it := el.Value.(*cacheItem)
		c.ll.Remove(el)
		delete(c.items, it.key)
		c.bytes -= len(it.data)
	}
}

// Len reports the number of cached values and their total bytes.
func (c *Cache) Len() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len(), c.bytes
}
