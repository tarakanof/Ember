package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestRunOnce_PostsAndDeletes(t *testing.T) {
	var posts, deletes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			atomic.AddInt32(&posts, 1)
		case http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, client)
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("want 1 POST, got %d", posts)
	}
	w.now = func() time.Time { return now.Add(200 * time.Second) }
	runOnce(context.Background(), w, client)
	if atomic.LoadInt32(&deletes) != 1 {
		t.Fatalf("want 1 DELETE, got %d", deletes)
	}
}

func TestRunOnce_WritesAndRemovesMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, client)
	marker := filepath.Join(w.cfg.StateDir, "u-123.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not written after POST: %v", err)
	}

	w.now = func() time.Time { return now.Add(200 * time.Second) } // age out -> DELETE
	runOnce(context.Background(), w, client)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker should be removed after reap, stat err = %v", err)
	}
}

// TestRunOnce_WarnsOnPostFailure is the Task-9 error-visibility regression
// test: a failing POST must produce a throttled slog Warn instead of being
// silently discarded (previously `_ = client.Post(ctx, req)`).
func TestRunOnce_WarnsOnPostFailure(t *testing.T) {
	daemonFailLog.Reset()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, client)
	if !strings.Contains(buf.String(), "kind=codex_post") {
		t.Errorf("expected a throttled codex_post warning, got: %s", buf.String())
	}
}
