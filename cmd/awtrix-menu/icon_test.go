package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIconForState_AllStatesDecodable(t *testing.T) {
	for _, s := range []string{"idle", "running", "waiting", "error"} {
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
