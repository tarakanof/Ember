package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestStatusRequest_OmitsNilPointers(t *testing.T) {
	req := StatusRequest{Source: "x", Tool: "claude", Session: "s", State: "running"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("context_pct")) {
		t.Errorf("JSON must not contain context_pct when nil, got: %s", b)
	}
	if bytes.Contains(b, []byte("source_color")) {
		t.Errorf("JSON must not contain source_color when nil, got: %s", b)
	}
}

func TestStatusRequest_SerializesNonNilPointers(t *testing.T) {
	pct := 42
	col := "#aa66ff"
	req := StatusRequest{
		Source:      "x",
		Tool:        "claude",
		Session:     "s",
		State:       "running",
		ContextPct:  &pct,
		SourceColor: &col,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"context_pct":42`)) {
		t.Errorf("JSON missing context_pct:42, got: %s", b)
	}
	if !bytes.Contains(b, []byte(`"source_color":"#aa66ff"`)) {
		t.Errorf("JSON missing source_color:#aa66ff, got: %s", b)
	}
}
