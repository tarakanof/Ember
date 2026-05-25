package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchBaseSessionLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/state" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"now":"2026-05-25T00:00:00Z","sessions":[
			{"source":"mbp","tool":"claude","session":"s1","state":"running","context_pct":73,"updated_at":"2026-05-25T00:00:00Z"}
		],"render":{}}`))
	}))
	defer srv.Close()

	base, live := fetchBaseSession(srv.URL, 2*time.Second)
	if !live {
		t.Fatal("expected live = true")
	}
	if base.ContextPct == nil || *base.ContextPct != 73 {
		t.Errorf("ContextPct = %v, want 73", base.ContextPct)
	}
	if base.State != "running" {
		t.Errorf("State = %q, want running", base.State)
	}
}

func TestFetchBaseSessionFallbackWhenUnreachable(t *testing.T) {
	base, live := fetchBaseSession("http://127.0.0.1:1", 200*time.Millisecond)
	if live {
		t.Error("expected live = false on unreachable host")
	}
	if base.State == "" {
		t.Error("sample base session must have a non-empty State")
	}
}

func TestFetchBaseSessionFallbackWhenNoSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"sessions":[],"render":{}}`))
	}))
	defer srv.Close()
	_, live := fetchBaseSession(srv.URL, 2*time.Second)
	if live {
		t.Error("empty session list -> not live, use sample")
	}
}

func TestFetchBaseSessionFallbackWhenEmptyURL(t *testing.T) {
	_, live := fetchBaseSession("", time.Second)
	if live {
		t.Error("empty URL -> not live")
	}
}
