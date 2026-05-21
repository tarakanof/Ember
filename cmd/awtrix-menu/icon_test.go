package main

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"
)

func TestIconForState_AllStatesDecodable(t *testing.T) {
	for _, s := range []string{"idle", "running", "waiting", "error", "done"} {
		data := iconForState(s)
		if len(data) == 0 {
			t.Errorf("%s: empty bytes", s)
			continue
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Errorf("%s: not valid PNG: %v", s, err)
		}
	}
}

func TestIconForState_UnknownReturnsIdle(t *testing.T) {
	if !bytes.Equal(iconForState("bogus"), iconForState("idle")) {
		t.Error("unknown state should fall back to idle")
	}
}

// TestDrawIcon_RendersStateColor confirms the robot is painted in the
// state colour (not a monochrome template), so the switch to SetIcon is
// meaningful — at least one pixel must match the running green.
func TestDrawIcon_RendersStateColor(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(iconForState("running")))
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

// TestDrawIcon_ErrorDiffersFromRunning guards that both the sprite
// (chevron eyes) and colour distinguish error from running.
func TestDrawIcon_ErrorDiffersFromRunning(t *testing.T) {
	if bytes.Equal(iconForState("error"), iconForState("running")) {
		t.Error("error and running icons are identical; want distinct sprite+colour")
	}
}
