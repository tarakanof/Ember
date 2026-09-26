package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func readLink(t *testing.T, path string) LinkState {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s LinkState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return s
}

func TestLinkStatusRecordsNoRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "claude-producer.link.json")
	l := NewLinkStatus(path)
	noRoute := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}
	l.Record(fmt.Errorf("Post \"http://x/v1/usage\": %w", noRoute))
	s := readLink(t, path)
	if s.OK || !s.NoRoute || s.Error == "" {
		t.Fatalf("want no-route failure, got %+v", s)
	}
	l.Record(nil)
	if s := readLink(t, path); !s.OK || s.NoRoute || s.Error != "" {
		t.Fatalf("want ok, got %+v", s)
	}
}

func TestLinkStatusRewritesOnlyOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.link.json")
	l := NewLinkStatus(path)
	t0 := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return t0 }
	l.Record(nil)
	l.now = func() time.Time { return t0.Add(time.Hour) }
	l.Record(nil)
	if s := readLink(t, path); !s.At.Equal(t0) {
		t.Fatalf("same state rewrote the file: at=%v", s.At)
	}
	l.Record(errors.New("connection refused"))
	if s := readLink(t, path); s.OK || s.NoRoute || !s.At.Equal(t0.Add(time.Hour)) {
		t.Fatalf("want other failure at t0+1h, got %+v", s)
	}
}

func TestNilLinkStatusIsANoOp(t *testing.T) {
	var l *LinkStatus
	l.Record(errors.New("x")) // must not panic
}

func TestClientRecordsLinkState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // an HTTP answer still means the link works
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "c.link.json")
	c := NewClient(srv.URL, "", time.Second).WithLinkStatus(NewLinkStatus(path))
	if err := c.Usage(context.Background(), UsageRequest{Tool: "claude"}); err == nil {
		t.Fatal("want the 401 as an error")
	}
	if s := readLink(t, path); !s.OK {
		t.Fatalf("an HTTP response should record ok, got %+v", s)
	}
}

func TestIsNoRoute(t *testing.T) {
	if !IsNoRoute(syscall.EHOSTUNREACH) || !IsNoRoute(errors.New("dial tcp 1.2.3.4:5: connect: no route to host")) {
		t.Fatal("want no route")
	}
	if IsNoRoute(nil) || IsNoRoute(syscall.ECONNREFUSED) {
		t.Fatal("want not no route")
	}
}
