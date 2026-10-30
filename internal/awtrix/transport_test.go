package awtrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func pushTwice(t *testing.T, rt http.RoundTripper) []string {
	t.Helper()
	var mu sync.Mutex
	var addrs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			return
		}
		mu.Lock()
		addrs = append(addrs, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cl := NewClientWithTransport(srv.URL, 2*time.Second, rt)
	for i := 0; i < 2; i++ {
		if err := cl.PushApp(context.Background(), "ember", map[string]any{"text": "x"}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), addrs...)
}

func TestClientWithTransportReusesConnectionByDefault(t *testing.T) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	defer tr.CloseIdleConnections()
	addrs := pushTwice(t, tr)
	if len(addrs) != 2 || addrs[0] != addrs[1] {
		t.Fatalf("remote addrs = %v, want one reused connection", addrs)
	}
}

func TestClientWithTransportNoKeepAliveDialsEachPush(t *testing.T) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	addrs := pushTwice(t, tr)
	if len(addrs) != 2 || addrs[0] == addrs[1] {
		t.Fatalf("remote addrs = %v, want a new connection per push", addrs)
	}
}
