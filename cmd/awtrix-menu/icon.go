package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

const iconSize = 18 // template icons are ~18px on macOS

var iconOnce sync.Once
var iconCache = map[string][]byte{}

func iconForState(state string) []byte {
	iconOnce.Do(func() {
		for _, s := range []string{"idle", "running", "waiting", "error"} {
			iconCache[s] = drawIcon(s)
		}
	})
	if b, ok := iconCache[state]; ok {
		return b
	}
	return iconCache["idle"]
}

func drawIcon(state string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	// Transparent background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{0, 0, 0, 0}}, image.Point{}, draw.Src)
	black := color.RGBA{0, 0, 0, 255}
	cx, cy := iconSize/2, iconSize/2
	r := iconSize/2 - 2
	switch state {
	case "running":
		// Filled circle
		drawDisc(img, cx, cy, r, black)
	case "waiting":
		// Outlined circle with central small dot
		drawCircle(img, cx, cy, r, black)
		drawDisc(img, cx, cy, 2, black)
	case "error":
		// Outlined circle with `!`
		drawCircle(img, cx, cy, r, black)
		// vertical stroke + dot for "!"
		for y := cy - r/2; y <= cy+r/4; y++ {
			img.Set(cx, y, black)
		}
		img.Set(cx, cy+r/2, black)
	default: // idle
		// Outlined circle, no fill
		drawCircle(img, cx, cy, r, black)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func drawCircle(img *image.RGBA, cx, cy, r int, c color.Color) {
	for x := -r; x <= r; x++ {
		for y := -r; y <= r; y++ {
			d2 := x*x + y*y
			if d2 >= (r-1)*(r-1) && d2 <= r*r {
				img.Set(cx+x, cy+y, c)
			}
		}
	}
}

func drawDisc(img *image.RGBA, cx, cy, r int, c color.Color) {
	for x := -r; x <= r; x++ {
		for y := -r; y <= r; y++ {
			if x*x+y*y <= r*r {
				img.Set(cx+x, cy+y, c)
			}
		}
	}
}
