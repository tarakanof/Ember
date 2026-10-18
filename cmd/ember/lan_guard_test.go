package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var errLANDialRefused = errors.New("test refused a non-loopback dial")

var (
	testLANGuard     *lanGuard
	guardedTransport *http.Transport
)

type lanRefusal struct {
	what, test string
}

type lanGuard struct {
	lookup func(ctx context.Context, host string) ([]net.IPAddr, error)

	mu      sync.Mutex
	refused []lanRefusal
	watched map[*testing.T]bool
}

func installLANGuard(rt http.RoundTripper) (*lanGuard, *http.Transport, http.RoundTripper, error) {
	tr, ok := rt.(*http.Transport)
	if !ok {
		return nil, nil, nil, fmt.Errorf("http.DefaultTransport is %T, want *http.Transport", rt)
	}
	g := &lanGuard{lookup: net.DefaultResolver.LookupIPAddr}
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		pinned, err := g.loopbackDialAddrs(ctx, addr)
		if err != nil {
			g.refuse(network+" "+addr, "")
			return nil, err
		}
		var errs []error
		for _, a := range pinned {
			conn, err := dial(ctx, network, a)
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
	return g, tr, &lanGuardTransport{guard: g, next: tr}, nil
}

func (g *lanGuard) loopbackDialAddrs(ctx context.Context, addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errLANDialRefused, addr)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return nil, fmt.Errorf("%w: %s", errLANDialRefused, addr)
		}
		return []string{addr}, nil
	}
	if !strings.EqualFold(host, "localhost") {
		return nil, fmt.Errorf("%w: %s", errLANDialRefused, addr)
	}
	ips, err := g.lookup(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%w: %s does not resolve", errLANDialRefused, host)
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if !ip.IP.IsLoopback() {
			return nil, fmt.Errorf("%w: %s resolves to %s", errLANDialRefused, host, ip.IP)
		}
		out = append(out, net.JoinHostPort(ip.IP.String(), port))
	}
	return out, nil
}

type lanGuardTransport struct {
	guard *lanGuard
	next  http.RoundTripper
}

func (l *lanGuardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !loopbackHost(r.URL.Hostname()) {
		l.guard.refuse(r.Method+" "+r.URL.String(), "")
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, errLANDialRefused
	}
	return l.next.RoundTrip(r)
}

func (g *lanGuard) localIPFor(host string) string {
	if net.ParseIP(host) == nil && !strings.EqualFold(host, "localhost") {
		g.refuse("udp route lookup of "+host, "")
		return ""
	}
	return outboundIP(host)
}

func (g *lanGuard) refuse(what, test string) {
	if test == "" {
		buf := make([]byte, 64<<10)
		test = stackTest(string(buf[:runtime.Stack(buf, false)]))
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refused = append(g.refused, lanRefusal{what: what, test: test})
}

func (g *lanGuard) watch(t *testing.T) (start int, first bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.watched[t] {
		return 0, false
	}
	if g.watched == nil {
		g.watched = map[*testing.T]bool{}
	}
	g.watched[t] = true
	return len(g.refused), true
}

func (g *lanGuard) since(n int) []lanRefusal {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]lanRefusal(nil), g.refused[min(n, len(g.refused)):]...)
}

func (g *lanGuard) report(w io.Writer) bool {
	all := g.since(0)
	if len(all) == 0 {
		return false
	}
	fmt.Fprintf(w, "FAIL: %d non-loopback dial(s) refused (#587); point the URL at an httptest stub:\n", len(all))
	for _, r := range all {
		fmt.Fprintf(w, "  %s from %s\n", r.what, r.origin())
	}
	return true
}

func (r lanRefusal) origin() string {
	if strings.HasPrefix(r.test, "Test") {
		return r.test
	}
	return r.test + " (GODEBUG=tracebackancestors=10 names its test)"
}

func watchLAN(t *testing.T) {
	t.Helper()
	t.Setenv("EMBER_TEST_LAN_WATCH", "1")
	if testLANGuard == nil {
		return
	}
	start, first := testLANGuard.watch(t)
	if !first {
		return
	}
	root, _, _ := strings.Cut(t.Name(), "/")
	t.Cleanup(func() {
		for _, r := range testLANGuard.since(start) {
			if strings.HasPrefix(r.test, "Test") && r.test != root {
				continue
			}
			t.Errorf("%s refused during %s, from %s", r.what, t.Name(), r.origin())
		}
	})
}

func stackTest(stack string) string {
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
	watchLAN(t)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = notFoundServer(t)
	return cfg
}

func guardApp(t *testing.T, a *App) *App {
	t.Helper()
	watchLAN(t)
	if testLANGuard != nil {
		a.localIPFor = testLANGuard.localIPFor
	}
	return a
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func freshLANGuard(t *testing.T) (*lanGuard, *http.Transport, http.RoundTripper, *[]string) {
	t.Helper()
	var dialed []string
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	g, guarded, rt, err := installLANGuard(tr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guarded.CloseIdleConnections)
	return g, guarded, rt, &dialed
}

func TestLANGuard_RefusesEveryNonLoopbackHost(t *testing.T) {
	g, tr, rt, dialed := freshLANGuard(t)
	ok := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ok.Close)
	for _, client := range []*http.Client{{Transport: rt}, {Transport: tr}} {
		for _, u := range []string{"http://192.0.2.1/", "http://[::ffff:192.0.2.1]/", "http://clock.local/", "http://10.0.0.5:8080/"} {
			if resp, err := client.Get(u); !errors.Is(err, errLANDialRefused) {
				if resp != nil {
					_ = resp.Body.Close()
				}
				t.Errorf("GET %s: err %v, want a refusal", u, err)
			}
		}
		resp, err := client.Get(ok.URL)
		if err != nil {
			t.Fatalf("loopback GET refused: %v", err)
		}
		_ = resp.Body.Close()
	}
	if n := len(g.since(0)); n != 8 {
		t.Errorf("recorded %d refusals, want 8", n)
	}
	for _, d := range *dialed {
		if !strings.HasPrefix(d, "127.0.0.1:") && !strings.HasPrefix(d, "[::1]:") {
			t.Errorf("dialled %s", d)
		}
	}
}

func fakeLookup(answers ...string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(context.Context, string) ([]net.IPAddr, error) {
		var out []net.IPAddr
		for _, s := range answers {
			out = append(out, net.IPAddr{IP: net.ParseIP(s)})
		}
		return out, nil
	}
}

func TestLANGuard_PinsLocalhostToValidatedLoopbackAddresses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved []string
		want     []string
	}{
		{"loopback", []string{"::1", "127.0.0.1"}, []string{"[::1]:80", "127.0.0.1:80"}},
		{"hosts file points elsewhere", []string{"192.0.2.1"}, nil},
		{"mixed answer", []string{"127.0.0.1", "192.0.2.1"}, nil},
		{"no answer", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &lanGuard{lookup: fakeLookup(tc.resolved...)}
			got, err := g.loopbackDialAddrs(t.Context(), "localhost:80")
			if tc.want == nil {
				if !errors.Is(err, errLANDialRefused) {
					t.Fatalf("dial addrs %q, err %v; want a refusal", got, err)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("dial addrs %q, err %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLANGuard_LocalhostFallsBackToTheNextLoopbackAddress(t *testing.T) {
	g, _, rt, dialed := freshLANGuard(t)
	g.lookup = fakeLookup("::1", "127.0.0.1")
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: rt}).Get("http://localhost:" + port + "/")
	if err != nil {
		t.Fatalf("localhost GET with ::1 first and a 127.0.0.1-only server: %v (dialled %v)", err, *dialed)
	}
	_ = resp.Body.Close()
	if !slices.Contains(*dialed, "127.0.0.1:"+port) {
		t.Fatalf("dialled %v, want a fallback to 127.0.0.1", *dialed)
	}
}

func TestLANGuard_CoversTheHealthcheckTLSTransport(t *testing.T) {
	g, tr, _, dialed := freshLANGuard(t)
	t.Setenv("EMBER_HEALTHCHECK_CA_FILE", "")
	t.Setenv("EMBER_HEALTHCHECK_INSECURE", "1")
	if err := healthcheckOnce("https://192.0.2.1/healthz", tr); !errors.Is(err, errLANDialRefused) {
		t.Fatalf("healthcheck to a LAN host: %v, want a refusal", err)
	}
	if len(g.since(0)) != 1 || len(*dialed) != 0 {
		t.Fatalf("refusals %v, dials %v; want one refusal and no dial", g.since(0), *dialed)
	}
}

func TestLANGuard_RefusesHostnameRouteLookups(t *testing.T) {
	g := &lanGuard{}
	if ip := g.localIPFor("clock.local"); ip != "" {
		t.Fatalf("route lookup of a hostname = %q, want it refused", ip)
	}
	if ip := g.localIPFor("127.0.0.1"); ip == "" {
		t.Fatal("route lookup of a loopback IP refused")
	}
	if got := g.since(0); len(got) != 1 || !strings.Contains(got[0].what, "clock.local") {
		t.Fatalf("refusals = %v, want the hostname lookup", got)
	}
}

func TestGuardedHelpersInjectTheRouteLookup(t *testing.T) {
	a := newTestApp(t)
	if reflect.ValueOf(a.localIPFor).Pointer() == reflect.ValueOf(outboundIP).Pointer() {
		t.Fatal("newTestApp left the real route lookup in place")
	}
	if b := NewApp(defaultConfig(), nil, discardLogger()); reflect.ValueOf(b.localIPFor).Pointer() != reflect.ValueOf(outboundIP).Pointer() {
		t.Fatal("NewApp must default to outboundIP")
	}
}

func isolatedGoRunEnv(t *testing.T) []string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"http":{"addr":"127.0.0.1:0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "EMBER_") || slices.Contains([]string{"CONFIG_PATH", "GOPROXY", "GOTOOLCHAIN"}, k) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GOPROXY=off", "GOTOOLCHAIN=local", "EMBER_CLOCK=off", "EMBER_MDNS_ADVERTISE=0", "CONFIG_PATH="+cfg)
}

func goRunCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", append([]string{"run", "."}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
