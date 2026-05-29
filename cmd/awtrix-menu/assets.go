package main

import (
	"bytes"
	"embed"
	"image"
	_ "image/png"
)

//go:embed assets/app-icons/*.png assets/tray/*.png
var assetsFS embed.FS

// appIconPalettes are the selectable macOS app-icon palettes (default first).
var appIconPalettes = []string{"multicolor-rgb", "cyan-green", "warm-amber", "aurora"}

// trayGlyphs are the selectable monochrome menu-bar template names.
var trayGlyphs = []string{"awtrix", "awtrix-screen", "aicode", "aicode-chat", "code", "code-hex", "pomodoro"}

// appIconPNG returns the embedded 512px PNG bytes for a palette.
func appIconPNG(palette string) ([]byte, error) {
	return assetsFS.ReadFile("assets/app-icons/" + palette + ".png")
}

// trayGlyphImage decodes the embedded template PNG (black + alpha) for a glyph.
func trayGlyphImage(name string) (image.Image, error) {
	b, err := assetsFS.ReadFile("assets/tray/" + name + ".png")
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}
