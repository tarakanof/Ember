package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTick_NoMarkers_NoOp(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("empty state dir should produce no traffic; posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
}

func TestTick_FreshMarker_RePosts(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running","message":"Bash"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 {
		t.Errorf("fresh marker should produce one POST; got %d", h.posts.Load())
	}
}

func TestTick_StaleMarker_RemovedAndDeleted(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(markerP, old, old); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.deletes.Load() != 1 {
		t.Errorf("stale marker should produce one DELETE; got %d", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("stale marker should be removed")
	}
}
