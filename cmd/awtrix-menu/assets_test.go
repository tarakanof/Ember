package main

import "testing"

func TestEmbeddedAssetsDecode(t *testing.T) {
	for _, p := range appIconPalettes {
		b, err := appIconPNG(p)
		if err != nil || len(b) == 0 {
			t.Errorf("app icon %q: err=%v len=%d", p, err, len(b))
		}
	}
	for _, g := range trayGlyphs {
		img, err := trayGlyphImage(g)
		if err != nil || img == nil {
			t.Errorf("tray glyph %q: err=%v img=%v", g, err, img)
		}
	}
}
