package main

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"
)

func TestIconFor_AllStatesDecodable(t *testing.T) {
	for _, tool := range []string{"claude", "codex"} {
		for _, s := range []string{"idle", "running", "waiting", "error", "done"} {
			data := iconFor(s, tool)
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
	if !bytes.Equal(iconFor("bogus", "claude"), iconFor("idle", "claude")) {
		t.Error("unknown state should fall back to idle")
	}
}

func TestIconFor_EmptyToolFallsBackToClaude(t *testing.T) {
	if !bytes.Equal(iconFor("running", ""), iconFor("running", "claude")) {
		t.Error("empty tool should render as claude")
	}
}

func TestIconFor_CodexDiffersFromClaude(t *testing.T) {
	if bytes.Equal(iconFor("running", "codex"), iconFor("running", "claude")) {
		t.Error("codex and claude running icons are identical; want distinct sprites")
	}
}

func TestDrawIcon_RendersStateColor(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(iconFor("running", "claude")))
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
	if bytes.Equal(iconFor("error", "claude"), iconFor("running", "claude")) {
		t.Error("error and running icons are identical; want distinct sprite+colour")
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
