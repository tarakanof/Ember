package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionHandler_PublicAndJSON(t *testing.T) {
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"binary", "revision", "dirty", "go_version"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing field %q (got %#v)", k, body)
		}
	}
	if body["binary"] != "awtrix-ai-status" {
		t.Errorf("binary = %v, want awtrix-ai-status", body["binary"])
	}
}
