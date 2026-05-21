package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dt/awtrix-ai-status/internal/producer"
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
