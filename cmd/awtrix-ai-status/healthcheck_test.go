package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deadAddr returns a URL whose host:port is known to be closed: it binds
// an ephemeral listener, captures the address, and closes the listener
// before returning. More deterministic than picking a "low" port like 1.
func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("listener.Close: %v", err)
	}
	return "http://" + addr + "/healthz"
}

func TestHealthcheckTarget_Default(t *testing.T) {
	t.Setenv("STATUS_HEALTHCHECK_URL", "")
	got := healthcheckTarget()
	want := "http://127.0.0.1:8080/healthz"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckTarget_Override(t *testing.T) {
	t.Setenv("STATUS_HEALTHCHECK_URL", "http://example.test/x")
	got := healthcheckTarget()
	want := "http://example.test/x"
	if got != want {
		t.Errorf("healthcheckTarget() = %q, want %q", got, want)
	}
}

func TestHealthcheckOnce_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := healthcheckOnce(srv.URL + "/healthz"); err != nil {
		t.Fatalf("healthcheckOnce: unexpected error: %v", err)
	}
}

func TestHealthcheckOnce_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := healthcheckOnce(srv.URL + "/healthz")
	if err == nil {
		t.Fatal("healthcheckOnce: expected error on 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("healthcheckOnce: error %q should mention status code 503", err)
	}
}

func TestHealthcheckOnce_Down(t *testing.T) {
	err := healthcheckOnce(deadAddr(t))
	if err == nil {
		t.Fatal("healthcheckOnce: expected error against closed port, got nil")
	}
}
