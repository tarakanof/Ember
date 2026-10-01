// Package discovery finds awtrix-ng clocks on the LAN and advertises the Ember
// server so the macOS app can find it.
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

type service struct {
	host    string
	baseURL string
}

const (
	awtrixServiceType = "_awtrixng._tcp.local."

	ngBoardType = "awtrixng"

	defaultProbeTimeout = 1500 * time.Millisecond
)

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

// Reachable reports whether baseURL is an awtrix-ng device responding right
// now, returning its firmware version.
func Reachable(ctx context.Context, cl *http.Client, baseURL string) (string, bool) {
	var timeout time.Duration
	if cl != nil {
		timeout = cl.Timeout
	}
	info, ok := probe(ctx, timeout, baseURL)
	return info.Version, ok
}

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
	udpFindPayload = "FIND_AWTRIXNG"
	udpFindPort    = 4210
	udpReplyPort   = 4211
)

func udpBrowse(ctx context.Context, timeout time.Duration) []service {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: udpReplyPort})
	if err != nil {
		return nil
	}
	defer conn.Close()

	for _, dst := range broadcastAddrs() {
		_, _ = conn.WriteToUDP([]byte(udpFindPayload), &net.UDPAddr{IP: dst, Port: udpFindPort})
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return collectUDPReplies(conn, deadline)
}

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

// BrowseAWTRIX browses the LAN for `timeout`, resolves _awtrixng._tcp
// instances, then fingerprints each and returns those that are awtrix-ng
// devices.
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
	_ = dnssd.LookupType(bctx, awtrixServiceType, func(e dnssd.BrowseEntry) { add(e) }, func(e dnssd.BrowseEntry) {})

	svcs := make([]service, 0, len(seen))
	for _, s := range seen {
		svcs = append(svcs, s)
	}
	if len(svcs) == 0 {
		svcs = udpBrowse(ctx, timeout)
	}
	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	defer pcancel()
	return filterCandidates(pctx, defaultProbeTimeout, svcs), nil
}
