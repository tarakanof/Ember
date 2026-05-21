package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if !serverReachable(srv.URL, time.Second) {
		t.Error("healthz 200 should be reachable")
	}
	if serverReachable("http://127.0.0.1:0", 200*time.Millisecond) {
		t.Error("unroutable server should be unreachable")
	}
	if serverReachable("", time.Second) {
		t.Error("empty URL should be unreachable")
	}
}
