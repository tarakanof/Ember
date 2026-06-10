package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeKeepsAwtrixRejectsOthers(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stats" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"uid":"awtrix_116ae8","version":"0.98","bat":100,"ram":120000}`))
	}))
	defer awtrix.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>not awtrix</html>`))
	}))
	defer other.Close()

	cl := &http.Client{Timeout: time.Second}
	if p, ok := probe(context.Background(), cl, awtrix.URL); !ok || p.UID != "awtrix_116ae8" || p.Version != "0.98" {
		t.Fatalf("awtrix probe: ok=%v p=%+v", ok, p)
	}
	if _, ok := probe(context.Background(), cl, other.URL); ok {
		t.Fatal("non-awtrix server should be rejected")
	}
}

func TestProbeRejectsEmptyUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.98"}`)) // no uid
	}))
	defer srv.Close()
	if _, ok := probe(context.Background(), &http.Client{Timeout: time.Second}, srv.URL); ok {
		t.Fatal("missing uid should be rejected")
	}
}

func TestFilterCandidatesBuildsBaseURL(t *testing.T) {
	awtrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"uid":"u1","version":"0.98"}`))
	}))
	defer awtrix.Close()
	got := filterCandidates(context.Background(), awtrix.Client(),
		[]service{{host: "awtrix_116ae8.local.", baseURL: awtrix.URL}})
	if len(got) != 1 || got[0].UID != "u1" || got[0].BaseURL != awtrix.URL || got[0].Host != "awtrix_116ae8.local." {
		t.Fatalf("filterCandidates = %+v", got)
	}
}

func TestBaseURLForPrefersIPv4(t *testing.T) {
	ips := []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.0.14")}
	if got := baseURLFor(ips, 80); got != "http://192.168.0.14:80" {
		t.Fatalf("baseURLFor = %q", got)
	}
	if got := baseURLFor(nil, 80); got != "" {
		t.Fatalf("baseURLFor(nil) = %q want empty", got)
	}
}
