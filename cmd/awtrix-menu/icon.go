package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

// robotNormal / robotError mirror the AWTRIX display sprites (10 wide ×
// 6 tall, 'X' = lit). Kept visually in sync with the robot in
// cmd/awtrix-ai-status/render.go: both arms, both eyes, four legs;
// error swaps in the 3-row chevron eye treatment.
var robotNormal = []string{
	".XXXXXXXX.",
	".X.XXXX.X.",
	".X.XXXX.X.",
	"XXXXXXXXXX",
	".XXXXXXXX.",
	".X.X..X.X.",
}

var robotError = []string{
	".XXXXXXXX.",
	".X.XXXX.X.",
	".XX.XX.XX.",
	"XX.XXXX.XX",
	".XXXXXXXX.",
	".X.X..X.X.",
}

// iconScale renders each sprite cell as an iconScale×iconScale block.
// 10×6 sprite × 3 → a 30×18 px icon, matching the prior ~18px menu-bar
// height.
const iconScale = 3

// stateColor returns the menu-bar robot colour for a state, matching the
// AWTRIX display palette. Idle uses a mid-grey that reads on both light
// and dark menu bars (the display's dimmer #666 is too faint on white).
func stateColor(state string) color.RGBA {
	switch state {
	case "running":
		return color.RGBA{0x2e, 0xe8, 0x5e, 0xff}
	case "waiting":
		return color.RGBA{0xff, 0xc1, 0x4d, 0xff}
	case "error":
		return color.RGBA{0xff, 0x3a, 0x3a, 0xff}
	case "done":
		return color.RGBA{0x4f, 0xa9, 0xff, 0xff}
	default: // idle / unknown
		return color.RGBA{0x88, 0x88, 0x88, 0xff}
	}
}

var iconOnce sync.Once
var iconCache = map[string][]byte{}

func iconForState(state string) []byte {
	iconOnce.Do(func() {
		for _, s := range []string{"idle", "running", "waiting", "error", "done"} {
			iconCache[s] = drawIcon(s)
		}
	})
	if b, ok := iconCache[state]; ok {
		return b
	}
	return iconCache["idle"]
}

func drawIcon(state string) []byte {
	sprite := robotNormal
	if state == "error" {
		sprite = robotError
	}
	c := stateColor(state)
	w := len(sprite[0]) * iconScale
	h := len(sprite) * iconScale
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{0, 0, 0, 0}}, image.Point{}, draw.Src)
	for cy, row := range sprite {
		for cx, ch := range row {
			if ch != 'X' {
				continue
			}
			for dy := 0; dy < iconScale; dy++ {
				for dx := 0; dx < iconScale; dx++ {
					img.Set(cx*iconScale+dx, cy*iconScale+dy, c)
				}
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
