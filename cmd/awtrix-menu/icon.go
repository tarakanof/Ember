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

// codexSprite is the Codex ">_" mark (10×6), kept pixel-identical to the copy
// in cmd/awtrix-ai-status/render.go (guarded by TestCodexSpriteCanonical in
// both packages). 2-px chevron (cols 0–3) + underscore (row 5, cols 5–9).
var codexSprite = []string{
	"XX........",
	".XX.......",
	"..XX......",
	"..XX......",
	".XX.......",
	"XX...XXXXX",
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

// tintAlpha returns a new RGBA image where every pixel is color c scaled by the
// source pixel's alpha (premultiplied). Used to paint a monochrome template
// glyph in a state color while keeping its shape/anti-aliasing.
func tintAlpha(src image.Image, c color.RGBA) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA() // 0..65535
			af := float64(a) / 65535.0
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(c.R) * af),
				G: uint8(float64(c.G) * af),
				B: uint8(float64(c.B) * af),
				A: uint8(af*255.0 + 0.5),
			})
		}
	}
	return out
}

func glyphForTool(tool string, p menuPrefs) string {
	switch tool {
	case "codex":
		return p.TrayCodexGlyph
	case "claude":
		return p.TrayClaudeGlyph
	default: // "" / idle / unknown
		return p.TrayIdleGlyph
	}
}

var iconMu sync.Mutex
var iconCache = map[string][]byte{}

// iconFor returns the PNG for the tray icon: the prefs glyph for the leading
// tool, tinted by the state color. Cached by glyph:state.
func iconFor(state, tool string, p menuPrefs) []byte {
	glyph := glyphForTool(tool, p)
	key := glyph + ":" + state
	iconMu.Lock()
	defer iconMu.Unlock()
	if b, ok := iconCache[key]; ok {
		return b
	}
	b := drawIcon(state, glyph)
	iconCache[key] = b
	return b
}

func drawIcon(state, glyph string) []byte {
	img, err := trayGlyphImage(glyph)
	if err != nil {
		return drawSpriteIcon(state) // last-resort: legacy robot sprite
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, tintAlpha(img, stateColor(state)))
	return buf.Bytes()
}

// drawSpriteIcon is the pre-template fallback: the robot sprite painted in the
// state color, kept so a missing/corrupt glyph asset never blanks the tray.
func drawSpriteIcon(state string) []byte {
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
