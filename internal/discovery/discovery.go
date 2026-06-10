// Package discovery finds AWTRIX clocks on the LAN via mDNS/DNS-SD and
// advertises the Ember server so the macOS app can find it. The browse/advertise
// wrappers are thin over brutella/dnssd; the HTTP fingerprint that decides
// whether a host is really an AWTRIX device is the unit-tested core.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/brutella/dnssd"
)

// Candidate is a discovered AWTRIX device on the LAN.
type Candidate struct {
	Host    string `json:"host"`     // mDNS host (e.g. "awtrix_116ae8.local.")
	BaseURL string `json:"base_url"` // http://<ip>:<port>
	UID     string `json:"uid"`
	Version string `json:"version"`
}

// service is a resolved mDNS instance reduced to what we probe.
type service struct {
	host    string
	baseURL string
}

type statsProbe struct {
	UID     string `json:"uid"`
	Version string `json:"version"`
}

// awtrixServiceType is the DNS-SD type AWTRIX3 advertises (a plain HTTP service).
const awtrixServiceType = "_http._tcp.local."

// probe GETs baseURL/api/stats and reports whether the host looks like an AWTRIX
// device. A non-empty uid is the fingerprint — ordinary HTTP servers don't carry
// one and won't decode into the stats shape.
func probe(ctx context.Context, cl *http.Client, baseURL string) (statsProbe, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/stats", nil)
	if err != nil {
		return statsProbe{}, false
	}
	resp, err := cl.Do(req)
	if err != nil {
		return statsProbe{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statsProbe{}, false
	}
	var p statsProbe
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return statsProbe{}, false
	}
	if p.UID == "" {
		return statsProbe{}, false
	}
	return p, true
}

// Reachable reports whether baseURL is an AWTRIX device responding right now.
// Used by the server to validate a configured clock URL before falling back to
// auto-discovery.
func Reachable(ctx context.Context, cl *http.Client, baseURL string) (string, bool) {
	p, ok := probe(ctx, cl, baseURL)
	return p.Version, ok
}

// filterCandidates probes every resolved host concurrently and keeps the AWTRIX
// ones. Probing in parallel (each bounded by the client timeout) keeps the total
// phase ≈ one probe regardless of how many _http._tcp hosts the LAN advertises.
// Results are index-ordered so output is deterministic.
func filterCandidates(ctx context.Context, cl *http.Client, svcs []service) []Candidate {
	found := make([]*Candidate, len(svcs))
	var wg sync.WaitGroup
	for i, s := range svcs {
		wg.Add(1)
		go func(i int, s service) {
			defer wg.Done()
			if p, ok := probe(ctx, cl, s.baseURL); ok {
				found[i] = &Candidate{Host: s.host, BaseURL: s.baseURL, UID: p.UID, Version: p.Version}
			}
		}(i, s)
	}
	wg.Wait()
	out := make([]Candidate, 0, len(svcs))
	for _, c := range found {
		if c != nil {
			out = append(out, *c)
		}
	}
	return out
}

// baseURLFor builds http://host:port, preferring an IPv4 literal (AWTRIX serves
// plain HTTP on port 80; IPv6 link-local addresses are unusable as-is).
func baseURLFor(ips []net.IP, port int) string {
	host := ""
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			host = v4.String()
			break
		}
	}
	if host == "" {
		for _, ip := range ips {
			if !ip.IsLinkLocalUnicast() {
				host = ip.String()
				break
			}
		}
	}
	if host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprint(port)))
}

// BrowseAWTRIX browses the LAN for `timeout`, resolves _http._tcp instances,
// then probes each and returns those that are AWTRIX devices. Requires multicast
// reachability (Docker host/macvlan networking).
func BrowseAWTRIX(ctx context.Context, timeout time.Duration) ([]Candidate, error) {
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	seen := map[string]service{}
	add := func(e dnssd.BrowseEntry) {
		port := e.Port
		if port == 0 {
			port = 80
		}
		if b := baseURLFor(e.IPs, port); b != "" {
			seen[e.Host] = service{host: e.Host, baseURL: b}
		}
	}
	// LookupType blocks until bctx ends; the deadline-exceeded error is expected.
	_ = dnssd.LookupType(bctx, awtrixServiceType, func(e dnssd.BrowseEntry) { add(e) }, func(e dnssd.BrowseEntry) {})

	svcs := make([]service, 0, len(seen))
	for _, s := range seen {
		svcs = append(svcs, s)
	}
	// Bound the probe phase separately: bctx is already spent on the lookup, so
	// reusing it would fail every probe. A fresh short budget caps total
	// discovery (lookup window + probe window) instead of running unbounded.
	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	defer pcancel()
	cl := &http.Client{Timeout: 1500 * time.Millisecond}
	return filterCandidates(pctx, cl, svcs), nil
}
