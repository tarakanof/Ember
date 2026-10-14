package producer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type strictStatus struct {
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
	State   string `json:"state"`
}

func strictOldServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var s strictStatus
		err := dec.Decode(&s)
		mu.Lock()
		if err != nil {
			seen = append(seen, "400")
		} else {
			seen = append(seen, "200")
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func resetOptionalFields(t *testing.T) {
	t.Helper()
	reset := func() {
		optionalFields.mu.Lock()
		optionalFields.until = map[string]time.Time{}
		optionalFields.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestPostResendsWithoutProjectToAStrictOldServer(t *testing.T) {
	resetOptionalFields(t)
	srv, seen := strictOldServer(t)
	c := NewClient(srv.URL, "", time.Second)
	req := StatusRequest{Source: "m4", Tool: "claude", Session: "s", State: "waiting", Project: "ember"}
	if err := c.Post(context.Background(), req); err != nil {
		t.Fatalf("post: %v", err)
	}
	if got := seen(); len(got) != 2 || got[0] != "400" || got[1] != "200" {
		t.Fatalf("requests = %v, want a 400 then a 200 without project", got)
	}
	if err := c.Post(context.Background(), req); err != nil {
		t.Fatalf("second post: %v", err)
	}
	if got := seen(); len(got) != 3 || got[2] != "200" {
		t.Fatalf("requests = %v, want the dropped field remembered", got)
	}
}

func TestPostDoesNotRetryOtherBadRequests(t *testing.T) {
	resetOptionalFields(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"state is required"}`))
	}))
	t.Cleanup(srv.Close)
	err := NewClient(srv.URL, "", time.Second).Post(context.Background(), StatusRequest{Project: "x"})
	var se *ServerError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}
