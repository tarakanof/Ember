package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestIconFor_AllStatesDecodable(t *testing.T) {
	for _, tool := range []string{"claude", "codex"} {
		for _, s := range []string{"idle", "running", "waiting", "error", "done"} {
			data := iconFor(s, tool, defaultMenuPrefs())
			if len(data) == 0 {
				t.Errorf("%s/%s: empty bytes", tool, s)
				continue
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Errorf("%s/%s: not valid PNG: %v", tool, s, err)
			}
		}
	}
}

func TestIconFor_UnknownReturnsIdle(t *testing.T) {
	if !bytes.Equal(iconFor("bogus", "claude", defaultMenuPrefs()), iconFor("idle", "claude", defaultMenuPrefs())) {
		t.Error("unknown state should fall back to idle")
	}
}

func TestIconFor_EmptyToolUsesIdleGlyph(t *testing.T) {
	p := defaultMenuPrefs() // claude=aicode, idle=awtrix (distinct glyphs)
	if bytes.Equal(iconFor("running", "", p), iconFor("running", "claude", p)) {
		t.Error("empty tool should use the idle glyph, distinct from the claude glyph")
	}
}

func TestIconFor_CodexDiffersFromClaude(t *testing.T) {
	if bytes.Equal(iconFor("running", "codex", defaultMenuPrefs()), iconFor("running", "claude", defaultMenuPrefs())) {
		t.Error("codex and claude running icons are identical; want distinct sprites")
	}
}

func TestDrawIcon_RendersStateColor(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(iconFor("running", "claude", defaultMenuPrefs())))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := stateColor("running")
	b := img.Bounds()
	found := false
	for y := b.Min.Y; y < b.Max.Y && !found; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			c := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
			if c == want {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("running icon contains no %v pixel — icon is not state-coloured", want)
	}
}

func TestDrawIcon_ErrorDiffersFromRunning(t *testing.T) {
	if bytes.Equal(iconFor("error", "claude", defaultMenuPrefs()), iconFor("running", "claude", defaultMenuPrefs())) {
		t.Error("error and running icons are identical; want distinct sprite+colour")
	}
}

func TestTintAlpha(t *testing.T) {
	// 2x1 source: left fully opaque black, right fully transparent.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255}) // alpha 255
	src.SetNRGBA(1, 0, color.NRGBA{0, 0, 0, 0})   // alpha 0
	red := color.RGBA{0xff, 0x00, 0x00, 0xff}

	out := tintAlpha(src, red)
	if got := out.RGBAAt(0, 0); got != (color.RGBA{0xff, 0, 0, 0xff}) {
		t.Errorf("opaque pixel = %+v, want full red", got)
	}
	if got := out.RGBAAt(1, 0); got != (color.RGBA{0, 0, 0, 0}) {
		t.Errorf("transparent pixel = %+v, want transparent", got)
	}
}

func TestCodexSpriteCanonical(t *testing.T) {
	want := []string{
		"XX........",
		".XX.......",
		"..XX......",
		"..XX......",
		".XX.......",
		"XX...XXXXX",
	}
	if len(codexSprite) != len(want) {
		t.Fatalf("codexSprite has %d rows, want %d", len(codexSprite), len(want))
	}
	for i := range want {
		if codexSprite[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, codexSprite[i], want[i])
		}
	}
}

func TestGlyphForTool(t *testing.T) {
	p := menuPrefs{TrayClaudeGlyph: "aicode", TrayCodexGlyph: "code", TrayIdleGlyph: "awtrix"}
	if glyphForTool("codex", p) != "code" {
		t.Error("codex glyph")
	}
	if glyphForTool("claude", p) != "aicode" {
		t.Error("claude glyph")
	}
	if glyphForTool("", p) != "awtrix" || glyphForTool("weird", p) != "awtrix" {
		t.Error("idle glyph for empty/unknown tool")
	}
}
