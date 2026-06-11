package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreviewEndpointOpenAndShaped(t *testing.T) {
	app := newPomodoroApp(t)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	// No /state sessions -> sample fallback (state "running", tool "claude").
	// Enable context number + pct so the ctx card appears alongside source.
	url := srv.URL + "/v1/preview?context_pct=true&context_number=true&rate_pct=false" +
		"&rate_bottom_bar=false&rate_reset=false&activity_detail=true&source_color=%23ff8800"

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
	if !seen["source"] || !seen["ctx"] {
		t.Fatalf("expected source and ctx cards, got %v", seen)
	}
	// Sample base session is running with activity enabled -> tool card present
	// -> Activity surfaced; "tool" must NOT appear as a grid frame.
	if seen["tool"] {
		t.Fatal("tool card must not be a grid frame")
	}
	if p.Activity == "" {
		t.Fatal("expected activity string for the sample running session")
	}
}
