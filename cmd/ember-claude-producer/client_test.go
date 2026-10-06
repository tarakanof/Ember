package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func slowServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewClient_UsesHookTimeout(t *testing.T) {
	srv := slowServer(t, 2*time.Second)
	c := NewClient(Config{Common: producer.Common{ServerURL: srv.URL}, HookTimeoutMs: 50})
	start := time.Now()
	if err := c.Post(context.Background(), StatusRequest{}); err == nil {
		t.Fatal("want a timeout error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("hook client waited %v, want ~HookTimeoutMs", d)
	}
}

func TestNewDaemonClient_IndependentOfHookTimeout(t *testing.T) {
	srv := slowServer(t, 200*time.Millisecond)
	c := NewDaemonClient(Config{Common: producer.Common{ServerURL: srv.URL}, HookTimeoutMs: 50})
	if err := c.Post(context.Background(), StatusRequest{}); err != nil {
		t.Errorf("daemon client gave up after HookTimeoutMs: %v", err)
	}
}
