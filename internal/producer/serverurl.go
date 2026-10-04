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

// AutoServerURL is the EMBER_SERVER_URL value (like an empty one) that asks
// the producer to find its server over mDNS.
const AutoServerURL = "auto"

// DefaultBrowseTimeout bounds one mDNS browse for _ember._tcp.
const DefaultBrowseTimeout = 3 * time.Second

const (
	// autoServerFailureThreshold is how many transport failures in a row make
	// a daemon browse again (the server may have moved to a new IP).
	autoServerFailureThreshold = 3
	// autoServerMinInterval spaces re-browses while the server stays down.
	autoServerMinInterval = time.Minute
)

// IsAutoServerURL reports whether v asks for discovery: empty or "auto".
func IsAutoServerURL(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, AutoServerURL)
}

// DiscoveredServer is one _ember._tcp instance.
type DiscoveredServer struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
}

func (s DiscoveredServer) String() string {
	return fmt.Sprintf("%s @ %s → %s", s.Name, dashIfBlank(s.Host), s.URL)
}

// ServerBrowser lists the Ember servers on the LAN; tests fake it.
type ServerBrowser func(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error)

// MDNSBrowser browses _ember._tcp with the server's own mDNS stack.
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

// ErrNoServer means the browse found no Ember server.
var ErrNoServer = errors.New("no Ember server found over mDNS (_ember._tcp)")

// AmbiguousServersError means several servers answered and nothing picks one.
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

// PickServer chooses one server. prefer (EMBER_SERVER_INSTANCE), when set,
// must match an instance name, host name or URL host; otherwise exactly one
// distinct server must have answered.
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

// ServerCache is the last discovered server, kept in the state dir so hooks
// (which never browse) and restarted daemons start from it.
type ServerCache struct {
	DiscoveredServer
	DiscoveredAt time.Time `json:"discovered_at"`
}

// ServerCachePath is ~/.local/state/ember/server.json.
func ServerCachePath(home string) string {
	return filepath.Join(home, ".local", "state", "ember", "server.json")
}

// ReadServerCache reads the cache; ok is false when missing or unreadable.
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

// WriteServerCache atomically replaces the cache.
func WriteServerCache(path string, c ServerCache) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server.json.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ResolveServerURL maps a configured EMBER_SERVER_URL to the URL to use now:
// an explicit URL as is, else the cached discovery ("" when none). auto
// reports whether discovery applies. It never browses, so hooks can call it.
func ResolveServerURL(home, configured string) (serverURL string, auto bool) {
	if !IsAutoServerURL(configured) {
		return strings.TrimSpace(configured), false
	}
	if c, ok := ReadServerCache(ServerCachePath(home)); ok {
		return c.URL, true
	}
	return "", true
}

// ServerLocator browses for the server and caches what it picks.
type ServerLocator struct {
	Browse    ServerBrowser
	Timeout   time.Duration // DefaultBrowseTimeout when zero
	CachePath string        // no caching when empty
	Prefer    string        // EMBER_SERVER_INSTANCE
	Now       func() time.Time
}

// NewServerLocator is the production locator for home.
func NewServerLocator(home, prefer string) *ServerLocator {
	return &ServerLocator{Browse: MDNSBrowser, CachePath: ServerCachePath(home), Prefer: prefer}
}

// Discover browses once, picks a server and caches it. A failure leaves the
// cache alone.
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
		_ = WriteServerCache(l.CachePath, ServerCache{DiscoveredServer: s, DiscoveredAt: now().UTC()})
	}
	return found, s, nil
}

// AutoServer is a daemon's discovered server URL: it re-browses after
// repeated transport failures, at most once per autoServerMinInterval.
type AutoServer struct {
	loc *ServerLocator
	now func() time.Time

	mu         sync.Mutex // protects url, failures, lastBrowse
	url        string
	failures   int
	lastBrowse time.Time
}

// NewAutoServer starts from initial (the cached URL, possibly "").
func NewAutoServer(loc *ServerLocator, initial string) *AutoServer {
	return &AutoServer{loc: loc, now: time.Now, url: initial}
}

// URL is the current server URL ("" until discovered).
func (a *AutoServer) URL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.url
}

// Ensure discovers the server when none is known yet.
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

// Report records one request's transport result (nil for any HTTP response).
// Context cancellation is not the server's fault and is ignored.
func (a *AutoServer) Report(err error) {
	if a == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	a.mu.Lock()
	if err == nil {
		a.failures = 0
		a.mu.Unlock()
		return
	}
	a.failures++
	due := a.failures >= autoServerFailureThreshold &&
		(a.lastBrowse.IsZero() || a.now().Sub(a.lastBrowse) >= autoServerMinInterval)
	if due {
		a.lastBrowse = a.now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*DefaultBrowseTimeout)
	defer cancel()
	s, berr := a.loc.Discover(ctx)
	if berr != nil {
		return
	}
	a.mu.Lock()
	a.url, a.failures = s.URL, 0
	a.mu.Unlock()
}

// WaitForServer discovers the server for a daemon whose URL is auto and
// uncached, retrying with backoff until found or ctx ends. logf reports each
// failed attempt.
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

// ServerReportInput is what ServerReport needs; Browse defaults to MDNSBrowser.
type ServerReportInput struct {
	Configured string // EMBER_SERVER_URL as written
	Prefer     string // EMBER_SERVER_INSTANCE
	Home       string
	Browse     ServerBrowser
}

// ServerReport is doctor's server section: the configured URL, and for
// discovery the cache, a fresh browse, the pick (cached) and reachability.
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

// DaemonServer prepares a daemon's server: nil for an explicit URL (ok when
// set); with discovery, an AutoServer seeded from the cache that first waits,
// retrying with backoff, until a server answers. ok is false when there is
// nothing to report to or ctx ended while waiting.
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

// TokenPlaceholder is the EMBER_TOKEN value the env template ships.
const TokenPlaceholder = "set-me-to-the-server-bearer-token"

// TokenHint is the install/doctor nudge when EMBER_TOKEN is unset or still
// the placeholder ("" when set).
func TokenHint(token string) string {
	if t := strings.TrimSpace(token); t != "" && t != TokenPlaceholder {
		return ""
	}
	return "EMBER_TOKEN is not set: put the server's bearer token in ~/.config/ember/producer.env (mode 0600) — writes are rejected without it"
}
