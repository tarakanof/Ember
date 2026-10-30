package awtrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type closeRT struct{}

func (closeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Close = true
	return http.DefaultTransport.RoundTrip(c)
}

func pushTwice(t *testing.T, rt http.RoundTripper) ([]string, []bool) {
	t.Helper()
	var mu sync.Mutex
	var addrs []string
	var closes []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			return
		}
		mu.Lock()
		addrs = append(addrs, r.RemoteAddr)
		closes = append(closes, r.Close)
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
	return append([]string(nil), addrs...), append([]bool(nil), closes...)
}

func TestClientWithNilTransportReusesConnection(t *testing.T) {
	addrs, closes := pushTwice(t, nil)
	if len(addrs) != 2 || addrs[0] != addrs[1] || closes[0] || closes[1] {
		t.Fatalf("addrs = %v closes = %v, want one reused connection", addrs, closes)
	}
}

func TestClientWithCloseTransportDialsEachPush(t *testing.T) {
	addrs, closes := pushTwice(t, closeRT{})
	if len(addrs) != 2 || addrs[0] == addrs[1] || !closes[0] || !closes[1] {
		t.Fatalf("addrs = %v closes = %v, want Connection: close and a new connection per push", addrs, closes)
	}
}
