package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var errLANDialRefused = errors.New("test refused a non-loopback dial")

type lanGuard struct {
	mu      sync.Mutex
	refused []string
}

func installLANGuard(rt http.RoundTripper) (*lanGuard, http.RoundTripper, error) {
	tr, ok := rt.(*http.Transport)
	if !ok {
		return nil, nil, fmt.Errorf("http.DefaultTransport is %T, want *http.Transport", rt)
	}
	g := &lanGuard{}
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !loopbackAddr(addr) {
			g.refuse(network + " " + addr)
			return nil, errLANDialRefused
		}
		return dial(ctx, network, addr)
	}
	return g, &lanGuardTransport{guard: g, next: tr}, nil
}

type lanGuardTransport struct {
	guard *lanGuard
	next  http.RoundTripper
}

func (l *lanGuardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !loopbackHost(r.URL.Hostname()) {
		l.guard.refuse(r.Method + " " + r.URL.String())
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, errLANDialRefused
	}
	return l.next.RoundTrip(r)
}

func (g *lanGuard) refuse(what string) {
	buf := make([]byte, 64<<10)
	where := dialOrigin(string(buf[:runtime.Stack(buf, false)]))
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refused = append(g.refused, what+" from "+where)
}

func (g *lanGuard) report(w io.Writer) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.refused) == 0 {
		return false
	}
	fmt.Fprintf(w, "FAIL: %d non-loopback dial(s) refused (#587); point the URL at an httptest stub (GODEBUG=tracebackancestors=10 names the test behind a background goroutine):\n", len(g.refused))
	for _, r := range g.refused {
		fmt.Fprintln(w, "  "+r)
	}
	return true
}

func dialOrigin(stack string) string {
	pkg := reflect.TypeFor[lanGuard]().PkgPath() + "."
	var first, created string
	for line := range strings.Lines(stack) {
		line = strings.TrimSpace(line)
		if c, ok := strings.CutPrefix(line, "created by "); ok && created == "" {
			created = c
		}
		fn, ok := strings.CutPrefix(line, pkg)
		if !ok {
			continue
		}
		if i := strings.LastIndex(fn, "("); i > 0 {
			fn = fn[:i]
		}
		if strings.HasPrefix(fn, "Test") {
			test, _, _ := strings.Cut(fn, ".")
			return test
		}
		if first == "" && !strings.Contains(fn, "lanGuard") {
			first = fn
		}
	}
	return fmt.Sprintf("a background goroutine in %s (created by %s)", first, created)
}

func notFoundServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func stubClockConfig(t *testing.T) Config {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = notFoundServer(t)
	return cfg
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return loopbackHost(host)
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
