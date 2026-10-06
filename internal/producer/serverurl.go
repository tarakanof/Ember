package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

const AutoServerURL = "auto"

const DefaultBrowseTimeout = 3 * time.Second

const (
	autoServerFailureThreshold = 3
	autoServerMinInterval      = time.Minute
)

func IsAutoServerURL(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, AutoServerURL)
}

type DiscoveredServer struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
}

func (s DiscoveredServer) String() string {
	return fmt.Sprintf("%s @ %s → %s", s.Name, dashIfBlank(s.Host), s.URL)
}

type ServerBrowser func(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error)

func MDNSBrowser(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error) {
	found, err := discovery.BrowseEmber(ctx, timeout)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveredServer, len(found))
	for i, f := range found {
		out[i] = DiscoveredServer{Name: f.Name, Host: f.Host, URL: f.URL, Version: f.Version}
	}
	return out, nil
}

var ErrNoServer = errors.New("no Ember server found over mDNS (_ember._tcp)")

type AmbiguousServersError struct {
	Servers []DiscoveredServer
}

func (e *AmbiguousServersError) Error() string {
	names := make([]string, len(e.Servers))
	for i, s := range e.Servers {
		names[i] = s.String()
	}
	return fmt.Sprintf("%d Ember servers found (%s); set EMBER_SERVER_URL, or EMBER_SERVER_INSTANCE to one's instance or host name, in ~/.config/ember/producer.env",
		len(e.Servers), strings.Join(names, "; "))
}

func PickServer(found []DiscoveredServer, prefer string) (DiscoveredServer, error) {
	var uniq []DiscoveredServer
	seen := map[string]bool{}
	for _, s := range found {
		if !seen[s.URL] {
			seen[s.URL] = true
			uniq = append(uniq, s)
		}
	}
	if len(uniq) == 0 {
		return DiscoveredServer{}, ErrNoServer
	}
	if prefer = strings.TrimSpace(prefer); prefer != "" {
		var match []DiscoveredServer
		for _, s := range uniq {
			if serverMatches(s, prefer) {
				match = append(match, s)
			}
		}
		switch len(match) {
		case 1:
			return match[0], nil
		case 0:
			return DiscoveredServer{}, fmt.Errorf("EMBER_SERVER_INSTANCE=%q matches none of the %d Ember server(s) found", prefer, len(uniq))
		default:
			return DiscoveredServer{}, &AmbiguousServersError{Servers: match}
		}
	}
	if len(uniq) > 1 {
		return DiscoveredServer{}, &AmbiguousServersError{Servers: uniq}
	}
	return uniq[0], nil
}

func serverMatches(s DiscoveredServer, prefer string) bool {
	host := strings.TrimSuffix(s.Host, ".")
	short, _, _ := strings.Cut(host, ".")
	urlHost := ""
	if u, err := url.Parse(s.URL); err == nil {
		urlHost = u.Hostname()
	}
	for _, c := range []string{s.Name, host, short, urlHost} {
		if c != "" && strings.EqualFold(c, prefer) {
			return true
		}
	}
	return false
}

type ServerCache struct {
	DiscoveredServer
	DiscoveredAt time.Time `json:"discovered_at"`
	Prefer       string    `json:"prefer,omitempty"`
}

func ServerCachePath(home string) string {
	return filepath.Join(StateHome(home), "ember", "server.json")
}

func ReadServerCache(path string) (ServerCache, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ServerCache{}, false
	}
	var c ServerCache
	if json.Unmarshal(b, &c) != nil || c.URL == "" {
		return ServerCache{}, false
	}
	return c, true
}

func WriteServerCache(path string, c ServerCache) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(path, b, 0o600)
}

func ResolveServerURL(home, configured, prefer string) (serverURL string, auto bool) {
	if !IsAutoServerURL(configured) {
		return strings.TrimSpace(configured), false
	}
	if c, ok := ReadServerCache(ServerCachePath(home)); ok && c.Prefer == strings.TrimSpace(prefer) {
		return c.URL, true
	}
	return "", true
}

type ServerLocator struct {
	Browse    ServerBrowser
	Timeout   time.Duration
	CachePath string
	Prefer    string
	Now       func() time.Time
}

func NewServerLocator(home, prefer string) *ServerLocator {
	return &ServerLocator{Browse: MDNSBrowser, CachePath: ServerCachePath(home), Prefer: prefer}
}

func (l *ServerLocator) Discover(ctx context.Context) (DiscoveredServer, error) {
	_, s, err := l.browseAndPick(ctx)
	return s, err
}

func (l *ServerLocator) browseAndPick(ctx context.Context) ([]DiscoveredServer, DiscoveredServer, error) {
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = DefaultBrowseTimeout
	}
	found, err := l.Browse(ctx, timeout)
	if err != nil {
		return nil, DiscoveredServer{}, fmt.Errorf("mDNS browse: %w", err)
	}
	s, err := PickServer(found, l.Prefer)
	if err != nil {
		return found, DiscoveredServer{}, err
	}
	if l.CachePath != "" {
		now := time.Now
		if l.Now != nil {
			now = l.Now
		}
		_ = WriteServerCache(l.CachePath, ServerCache{DiscoveredServer: s, DiscoveredAt: now().UTC(), Prefer: strings.TrimSpace(l.Prefer)})
	}
	return found, s, nil
}

type AutoServer struct {
	loc *ServerLocator
	now func() time.Time

	mu         sync.Mutex
	url        string
	failures   int
	lastBrowse time.Time
	browsing   sync.WaitGroup
}

func NewAutoServer(loc *ServerLocator, initial string) *AutoServer {
	return &AutoServer{loc: loc, now: time.Now, url: initial}
}

func (a *AutoServer) URL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.url
}

func (a *AutoServer) Ensure(ctx context.Context) (string, error) {
	if u := a.URL(); u != "" {
		return u, nil
	}
	s, err := a.loc.Discover(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastBrowse = a.now()
	if err != nil {
		return "", err
	}
	a.url, a.failures = s.URL, 0
	return a.url, nil
}

func (a *AutoServer) Report(err error) {
	if a == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.failures = 0
		return
	}
	a.failures++
	if a.failures < autoServerFailureThreshold ||
		(!a.lastBrowse.IsZero() && a.now().Sub(a.lastBrowse) < autoServerMinInterval) {
		return
	}
	a.lastBrowse = a.now()
	a.browsing.Add(1)
	go func() {
		defer a.browsing.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*DefaultBrowseTimeout)
		defer cancel()
		s, berr := a.loc.Discover(ctx)
		if berr != nil {
			return
		}
		a.mu.Lock()
		a.url, a.failures = s.URL, 0
		a.mu.Unlock()
	}()
}

func WaitForServer(ctx context.Context, a *AutoServer, logf func(err error, retryIn time.Duration)) (string, error) {
	backoff := 5 * time.Second
	for {
		u, err := a.Ensure(ctx)
		if err == nil {
			return u, nil
		}
		if logf != nil {
			logf(err, backoff)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
	}
}

type ServerReportInput struct {
	Configured string
	Prefer     string
	Home       string
	Browse     ServerBrowser
}

func ServerReport(ctx context.Context, in ServerReportInput) []string {
	if !IsAutoServerURL(in.Configured) {
		u := strings.TrimSpace(in.Configured)
		return []string{"server: " + reachability(ctx, u)}
	}
	lines := []string{"server_url: auto (mDNS _ember._tcp) — EMBER_SERVER_URL empty or \"auto\""}
	if in.Prefer != "" {
		lines = append(lines, fmt.Sprintf("  prefer: EMBER_SERVER_INSTANCE=%q", in.Prefer))
	}
	cachePath := ServerCachePath(in.Home)
	if c, ok := ReadServerCache(cachePath); ok {
		lines = append(lines, fmt.Sprintf("  cached: %s (discovered %s)", c.DiscoveredServer, c.DiscoveredAt.Local().Format(time.RFC3339)))
	} else {
		lines = append(lines, "  cached: (none)")
	}
	browse := in.Browse
	if browse == nil {
		browse = MDNSBrowser
	}
	loc := &ServerLocator{Browse: browse, CachePath: cachePath, Prefer: in.Prefer}
	found, pick, err := loc.browseAndPick(ctx)
	if found != nil || err == nil || errors.Is(err, ErrNoServer) {
		lines = append(lines, fmt.Sprintf("  mDNS browse (%s): found %d", DefaultBrowseTimeout, len(found)))
		for _, s := range found {
			v := ""
			if s.Version != "" {
				v = " (version " + s.Version + ")"
			}
			lines = append(lines, "    "+s.String()+v)
		}
	}
	switch {
	case err == nil:
		lines = append(lines, "  using "+pick.URL+" (cached for hooks and daemons)",
			"server: "+reachability(ctx, pick.URL))
	case errors.Is(err, ErrNoServer):
		lines = append(lines, "server: NOT FOUND — is the server up with EMBER_MDNS_ADVERTISE on, and its container on host networking "+
			"(multicast doesn't cross the Docker bridge)? Same LAN/VLAN as this machine? Else set EMBER_SERVER_URL explicitly.")
	default:
		lines = append(lines, "server: "+err.Error())
	}
	return lines
}

func reachability(ctx context.Context, u string) string {
	if u == "" {
		return "(no server_url configured)"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"/healthz", nil)
	if err != nil {
		return fmt.Sprintf("INVALID URL (%s): %v", u, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("UNREACHABLE (%s)", u)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("UNHEALTHY (%s: %s)", u, resp.Status)
	}
	return fmt.Sprintf("reachable (%s)", u)
}

func DaemonServer(ctx context.Context, home, serverURL string, auto bool, prefer string) (*AutoServer, bool) {
	if !auto {
		return nil, serverURL != ""
	}
	a := NewAutoServer(NewServerLocator(home, prefer), serverURL)
	if _, err := WaitForServer(ctx, a, func(err error, retryIn time.Duration) {
		slog.Warn("server discovery failed", "err", err, "retry_in", retryIn)
	}); err != nil {
		return nil, false
	}
	slog.Info("server discovered", "url", a.URL())
	return a, true
}

const TokenPlaceholder = "set-me-to-the-server-bearer-token"

func TokenHint(token string) string {
	if t := strings.TrimSpace(token); t != "" && t != TokenPlaceholder {
		return ""
	}
	return "EMBER_TOKEN is not set: put the server's bearer token in ~/.config/ember/producer.env (mode 0600) — writes are rejected without it"
}
