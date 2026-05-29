package render

import "testing"

func TestHexRGBParsesAndRejects(t *testing.T) {
	c, ok := HexRGB("#FF8800")
	if !ok || c != (RGB{R: 0xff, G: 0x88, B: 0x00}) {
		t.Fatalf("HexRGB(#FF8800) = %+v, %v", c, ok)
	}
	if _, ok := HexRGB("orange"); ok {
		t.Fatal("HexRGB(orange) should fail")
	}
}
