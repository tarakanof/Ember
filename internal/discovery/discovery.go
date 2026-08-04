// Package discovery finds awtrix-ng clocks on the LAN and advertises the Ember
// server so the macOS app can find it. mDNS/DNS-SD is the primary path; a
// FIND_AWTRIXNG UDP broadcast is the fallback for networks where multicast
// doesn't make it through. The browse/advertise wrappers are thin over
// brutella/dnssd; the HTTP fingerprint that decides whether a host is really an
// awtrix-ng device — and the UDP reply parsing — are the unit-tested core.
package discovery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutella/dnssd"

	"github.com/tarakanof/ember/internal/awtrix"
)

// Candidate is a discovered awtrix-ng device on the LAN.
type Candidate struct {
	Host    string `json:"host"`     // mDNS host or UDP-reported hostname (e.g. "Awtrix")
	BaseURL string `json:"base_url"` // http://<ip>:<port>
	UID     string `json:"uid"`
	Version string `json:"version"`
}

// service is a resolved instance (mDNS or UDP) reduced to what we probe.
type service struct {
	host    string
	baseURL string
}

const (
	// awtrixServiceType is the DNS-SD type awtrix-ng advertises. AWTRIX3 had no
	// type of its own and was found by browsing generic _http._tcp; NG registers
	// this one, so the browse no longer sweeps every web server on the LAN.
	awtrixServiceType = "_awtrixng._tcp.local."

	// ngBoardType is the boardType GET /api/v1/device reports on awtrix-ng.
	ngBoardType = "awtrixng"

	// defaultProbeTimeout bounds one fingerprint request when the caller didn't
	// supply a client timeout.
	defaultProbeTimeout = 1500 * time.Millisecond
)

// probe fingerprints baseURL via GET /api/v1/device (internal/awtrix). A host is
// an awtrix-ng clock when it reports a non-empty uid AND boardType "awtrixng":
// the uid alone is not enough now that the path is a documented, generic-looking
// API, and the legacy AWTRIX3 /api/stats fingerprint is gone from NG entirely.
func probe(ctx context.Context, timeout time.Duration, baseURL string) (awtrix.DeviceInfo, bool) {
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	info, err := awtrix.NewClient(baseURL, timeout).DeviceInfo(ctx)
	if err != nil || info.UID == "" || info.BoardType != ngBoardType {
		return awtrix.DeviceInfo{}, false
	}
	return info, true
}

// Reachable reports whether baseURL is an awtrix-ng device responding right now,
// returning its firmware version. Used by the server to validate a configured
// clock URL before falling back to auto-discovery. Only cl's Timeout is honoured
// — the fingerprint goes through internal/awtrix, which owns its own transport.
func Reachable(ctx context.Context, cl *http.Client, baseURL string) (string, bool) {
	var timeout time.Duration
	if cl != nil {
		timeout = cl.Timeout
	}
	info, ok := probe(ctx, timeout, baseURL)
	return info.Version, ok
}

// filterCandidates probes every resolved host concurrently and keeps the
// awtrix-ng ones. Probing in parallel (each bounded by timeout) keeps the total
// phase ≈ one probe regardless of how many hosts answered the browse. Results
// are index-ordered so output is deterministic.
func filterCandidates(ctx context.Context, timeout time.Duration, svcs []service) []Candidate {
	found := make([]*Candidate, len(svcs))
	var wg sync.WaitGroup
	for i, s := range svcs {
		wg.Add(1)
		go func(i int, s service) {
			defer wg.Done()
			if info, ok := probe(ctx, timeout, s.baseURL); ok {
				found[i] = &Candidate{Host: s.host, BaseURL: s.baseURL, UID: info.UID, Version: info.Version}
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

// baseURLFor builds http://host:port, preferring an IPv4 literal (the clock
// serves plain HTTP on port 80; IPv6 link-local addresses are unusable as-is).
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

const (
	// udpFindPayload is the ASCII probe awtrix-ng answers on udpFindPort.
	udpFindPayload = "FIND_AWTRIXNG"
	udpFindPort    = 4210
	// udpReplyPort is where the device sends its answer. Verified against
	// firmware 1.0.13: the reply goes to this FIXED port, not to the probe's
	// source port, so the collector must own 4211 before broadcasting.
	udpReplyPort = 4211
)

// udpBrowse broadcasts FIND_AWTRIXNG to udpFindPort and collects replies on
// udpReplyPort until timeout elapses. Used only when the mDNS browse comes up
// empty (multicast blocked, or a Docker bridge in the way). Returns nil — never
// an error — because it is a best-effort fallback: a bound-port conflict or a
// network with no broadcast route is indistinguishable from "no clock here".
func udpBrowse(ctx context.Context, timeout time.Duration) []service {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: udpReplyPort})
	if err != nil {
		return nil
	}
	defer conn.Close()

	// 255.255.255.255 alone is unreliable on multi-interface hosts (macOS picks
	// an interface by route and the packet can leave the wrong one), so the
	// per-interface directed broadcast addresses go out too.
	for _, dst := range broadcastAddrs() {
		_, _ = conn.WriteToUDP([]byte(udpFindPayload), &net.UDPAddr{IP: dst, Port: udpFindPort})
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return collectUDPReplies(conn, deadline)
}

// collectUDPReplies reads FIND_AWTRIXNG answers off conn until deadline. The
// reply's SOURCE IP is the device address — the hostname it reports may not be
// resolvable (NG lets the device be renamed without its mDNS record following),
// so it is kept only as the display host.
func collectUDPReplies(conn *net.UDPConn, deadline time.Time) []service {
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil
	}
	seen := map[string]service{}
	buf := make([]byte, 512)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		host, port := parseUDPReply(string(buf[:n]))
		if host == "" {
			continue
		}
		base := fmt.Sprintf("http://%s", net.JoinHostPort(addr.IP.String(), strconv.Itoa(port)))
		seen[base] = service{host: host, baseURL: base}
	}
	out := make([]service, 0, len(seen))
	for _, s := range seen {
		out = append(out, s)
	}
	return out
}

// parseUDPReply splits a FIND_AWTRIXNG reply body into hostname and web port.
// The body is "<hostname>" (port 80 implied) or "<hostname>:<port>" when the web
// UI isn't on 80. An empty host or unparseable port yields ("", 0).
func parseUDPReply(body string) (string, int) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", 0
	}
	host, portStr, hasPort := strings.Cut(body, ":")
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0
	}
	if !hasPort {
		return host, 80
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1 || port > 65535 {
		return "", 0
	}
	return host, port
}

// broadcastAddrs returns 255.255.255.255 plus the directed broadcast address of
// every up, broadcast-capable interface's IPv4 network.
func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4, mask := n.IP.To4(), net.IP(n.Mask).To4()
			if ip4 == nil || mask == nil {
				continue
			}
			b := make(net.IP, net.IPv4len)
			for i := range b {
				b[i] = ip4[i] | ^mask[i]
			}
			out = append(out, b)
		}
	}
	return out
}

// BrowseAWTRIX browses the LAN for `timeout`, resolves _awtrixng._tcp instances,
// then fingerprints each and returns those that are awtrix-ng devices. When the
// browse resolves nothing it retries over the UDP broadcast fallback (another
// `timeout` window). mDNS requires multicast reachability (Docker host/macvlan
// networking); the UDP path only needs a broadcast route.
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
	if len(svcs) == 0 {
		svcs = udpBrowse(ctx, timeout)
	}
	// Bound the probe phase separately: bctx is already spent on the lookup, so
	// reusing it would fail every probe. A fresh short budget caps total
	// discovery (lookup window + probe window) instead of running unbounded.
	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	defer pcancel()
	return filterCandidates(pctx, defaultProbeTimeout, svcs), nil
}
