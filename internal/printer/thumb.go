package printer

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
)

type ThumbKind string

const (
	ThumbSolid ThumbKind = "solid"
	ThumbGhost ThumbKind = "ghost"
)

const (
	ThumbRecipe    = "r1"
	ThumbSize      = 466
	MaxThumbSidePx = 2048

	thumbRadius    = 208.0
	thumbRimInner  = 170.0
	thumbGhostGain = 0.25
	thumbAlphaCut  = 8
	thumbPad       = 0.04
	thumbQuality   = 80
	thumbMaxTaps   = 8
)

var ErrThumbKind = errors.New("kind must be solid or ghost")

func CheckThumb(b []byte) error {
	_, _, err := decodeThumb(b)
	return err
}

func decodeThumb(b []byte) (*image.RGBA, image.Rectangle, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("%w: %v", ErrBadThumbnail, err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxThumbSidePx || cfg.Height > MaxThumbSidePx {
		return nil, image.Rectangle{}, fmt.Errorf("%w: %dx%d px", ErrBadThumbnail, cfg.Width, cfg.Height)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("%w: %v", ErrBadThumbnail, err)
	}
	src := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(src, src.Rect, img, img.Bounds().Min, draw.Src)
	box := contentBox(src)
	if box.Empty() {
		return nil, image.Rectangle{}, fmt.Errorf("%w: fully transparent", ErrBadThumbnail)
	}
	return src, box, nil
}

func BakeThumb(b []byte, kind ThumbKind) ([]byte, error) {
	if kind != ThumbSolid && kind != ThumbGhost {
		return nil, ErrThumbKind
	}
	src, box, err := decodeThumb(b)
	if err != nil {
		return nil, err
	}
	out := bakeDisc(src, box, kind)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	return buf.Bytes(), nil
}

func contentBox(src *image.RGBA) image.Rectangle {
	var box image.Rectangle
	r := src.Rect
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if src.Pix[src.PixOffset(x, y)+3] >= thumbAlphaCut {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

func bakeDisc(src *image.RGBA, box image.Rectangle, kind ThumbKind) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, ThumbSize, ThumbSize))
	for i := 3; i < len(out.Pix); i += 4 {
		out.Pix[i] = 255
	}
	w, h := float64(box.Dx()), float64(box.Dy())
	scale := 2 * thumbRadius / (math.Hypot(w, h) * (1 + 2*thumbPad))
	c := float64(ThumbSize) / 2
	cx, cy := float64(box.Min.X)+w/2, float64(box.Min.Y)+h/2
	taps := min(max(int(math.Ceil(1/scale)), 1), thumbMaxTaps)
	for y := range ThumbSize {
		for x := range ThumbSize {
			k := discGain(float64(x)+0.5-c, float64(y)+0.5-c)
			if k == 0 {
				continue
			}
			var acc [3]float64
			for sy := range taps {
				for sx := range taps {
					dx := float64(x) + (float64(sx)+0.5)/float64(taps) - c
					dy := float64(y) + (float64(sy)+0.5)/float64(taps) - c
					p := bilinear(src, cx+dx/scale, cy+dy/scale)
					for ch := range 3 {
						acc[ch] += p[ch]
					}
				}
			}
			n := float64(taps * taps)
			rgb := [3]float64{acc[0] / n, acc[1] / n, acc[2] / n}
			if kind == ThumbGhost {
				l := (0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]) * thumbGhostGain
				rgb = [3]float64{l, l, l}
			}
			o := out.PixOffset(x, y)
			for ch := range 3 {
				out.Pix[o+ch] = uint8(min(max(rgb[ch]*k+0.5, 0), 255))
			}
		}
	}
	return out
}

func discGain(dx, dy float64) float64 {
	d := math.Hypot(dx, dy)
	edge := min(max(thumbRadius-d+0.5, 0), 1)
	if edge == 0 || d <= thumbRimInner {
		return edge
	}
	return edge * (1 - min((d-thumbRimInner)/(thumbRadius-thumbRimInner), 1))
}

func bilinear(src *image.RGBA, fx, fy float64) [3]float64 {
	r := src.Rect
	fx, fy = fx-0.5, fy-0.5
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	tx, ty := fx-float64(x0), fy-float64(y0)
	at := func(x, y int) [3]float64 {
		if x < r.Min.X || y < r.Min.Y || x >= r.Max.X || y >= r.Max.Y {
			return [3]float64{}
		}
		o := src.PixOffset(x, y)
		return [3]float64{float64(src.Pix[o]), float64(src.Pix[o+1]), float64(src.Pix[o+2])}
	}
	a, b, c, d := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
	var out [3]float64
	for ch := range 3 {
		top := a[ch]*(1-tx) + b[ch]*tx
		bot := c[ch]*(1-tx) + d[ch]*tx
		out[ch] = top*(1-ty) + bot*ty
	}
	return out
}
