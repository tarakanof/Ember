package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreviewSourceCardDefaultOnAndOff(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// Common params — no source_card or session_bar; deprecated params are ignored.
	oldParams := "context_pct=false&rate_bottom_bar=false&activity_detail=false"

	decodeFrames := func(rawURL string) []struct {
		Card   string   `json:"card"`
		Pixels []string `json:"pixels"`
	} {
		t.Helper()
		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET %s: %v", rawURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var p struct {
			Frames []struct {
				Card   string   `json:"card"`
				Pixels []string `json:"pixels"`
			} `json:"frames"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return p.Frames
	}

	hasCard := func(frames []struct {
		Card   string   `json:"card"`
		Pixels []string `json:"pixels"`
	}, name string) bool {
		for _, f := range frames {
			if f.Card == name {
				return true
			}
		}
		return false
	}

	// 1) Old client: no source_card param → defaults to true → source card present.
	frames := decodeFrames(srv.URL + "/v1/preview?" + oldParams)
	if !hasCard(frames, "source") {
		t.Fatalf("case 1 (no source_card param): expected source card, got %v",
			func() []string {
				var names []string
				for _, f := range frames {
					names = append(names, f.Card)
				}
				return names
			}())
	}

	// 2) Explicit source_card=false → source card absent.
	frames = decodeFrames(srv.URL + "/v1/preview?" + oldParams + "&source_card=false")
	if hasCard(frames, "source") {
		t.Fatal("case 2 (source_card=false): source card should be absent")
	}

	// 3) session_bar=false + rate_bottom_bar=false → bottom bar row (row 7) all black.
	//    Use source card (always present via default source_card=true) for pixel check.
	frames = decodeFrames(srv.URL + "/v1/preview?" + oldParams + "&session_bar=false")
	var sourcePixels []string
	for _, f := range frames {
		if f.Card == "source" {
			sourcePixels = f.Pixels
			break
		}
	}
	if sourcePixels == nil {
		t.Fatal("case 3: source card not found for pixel check")
	}
	// Row 7 = pixels [224..255] (32 pixels × row index 7).
	for i := 224; i < 256; i++ {
		if sourcePixels[i] != "#000000" {
			t.Fatalf("case 3 (session_bar=false): row 7 pixel %d = %q, want #000000", i, sourcePixels[i])
		}
	}
}

func TestPreviewEndpointOpenAndShaped(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// No /state sessions -> sample fallback (state "running", tool "claude").
	// Enable context_pct and activity; deprecated params (rate_pct, context_number,
	// rate_reset) are silently ignored.
	url := srv.URL + "/v1/preview?context_pct=true&rate_bottom_bar=false" +
		"&activity_detail=true&source_color=%23ff8800"

	resp, err := http.Get(url) // no Authorization header: endpoint must be open
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var p struct {
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Activity string `json:"activity"`
		Frames   []struct {
			Card   string   `json:"card"`
			Pixels []string `json:"pixels"`
		} `json:"frames"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Width != 32 || p.Height != 8 {
		t.Fatalf("dims = %dx%d", p.Width, p.Height)
	}
	if len(p.Frames) == 0 {
		t.Fatal("expected at least one frame")
	}
	seen := map[string]bool{}
	for _, f := range p.Frames {
		seen[f.Card] = true
		if len(f.Pixels) != 256 {
			t.Fatalf("card %s pixels = %d, want 256", f.Card, len(f.Pixels))
		}
	}
	// Source card is always present; the tool card is excluded from Frames (no static grid).
	// Usage cards are present because usage_card defaults true.
	if !seen["source"] {
		t.Fatalf("expected source card, got %v", seen)
	}
	if seen["tool"] {
		t.Fatal("tool card must not be a grid frame")
	}
	if p.Activity == "" {
		t.Fatal("expected activity string for the sample running session")
	}
}
