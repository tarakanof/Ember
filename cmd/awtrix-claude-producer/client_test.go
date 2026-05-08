package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_PostSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, Token: "secret-token", HookTimeoutMs: 1000})
	if err := c.Post(context.Background(), StatusRequest{
		Source: "x", Tool: "claude", Session: "s", State: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
}

func TestClient_PostNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, Token: "", HookTimeoutMs: 1000})
	if err := c.Post(context.Background(), StatusRequest{
		Source: "x", Tool: "claude", Session: "s", State: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty when token unset", gotAuth)
	}
}

func TestClient_DeleteSendsBody(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, HookTimeoutMs: 1000})
	if err := c.Delete(context.Background(), DeleteRequest{
		Source: "dt-mbp", Tool: "claude", Session: "s",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"source":"dt-mbp"`) {
		t.Errorf("DELETE body missing source: %q", body)
	}
}

func TestClient_NonSuccessReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, HookTimeoutMs: 1000})
	err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running"})
	if err == nil {
		t.Error("401 response should produce error")
	}
}

func TestClient_EmptyServerURLErrors(t *testing.T) {
	c := NewClient(Config{ServerURL: "", HookTimeoutMs: 1000})
	err := c.Post(context.Background(), StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running"})
	if err == nil {
		t.Error("empty server URL should error")
	}
}
