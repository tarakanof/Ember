package discovery

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/brutella/dnssd"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

type EmberServer struct {
	Name    string
	Host    string
	URL     string
	Version string
}

const emberBrowseName = emberServiceType + ".local."

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

func BrowseEmber(ctx context.Context, timeout time.Duration) ([]EmberServer, error) {
	return browseEmber(ctx, timeout, legacyUnicastBrowse, lookupType)
}

type unicastLookup func(ctx context.Context) ([]EmberServer, error)

func browseEmber(ctx context.Context, timeout time.Duration, lookupUnicast unicastLookup, lookupMDNS mdnsLookup) ([]EmberServer, error) {
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		mu    sync.Mutex
		seen  = map[string]EmberServer{}
		order []string
	)
	add := func(s EmberServer) {
		mu.Lock()
		defer mu.Unlock()
		key := s.Name + "\x00" + s.URL
		if _, dup := seen[key]; !dup {
			order = append(order, key)
			seen[key] = s
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var unicastErr, multicastErr error
	go func() {
		defer wg.Done()
		var found []EmberServer
		found, unicastErr = lookupUnicast(bctx)
		for _, s := range found {
			add(s)
		}
	}()
	go func() {
		defer wg.Done()
		err := lookupMDNS(bctx, emberBrowseName, func(e dnssd.BrowseEntry) {
			if s, ok := emberServerFrom(e); ok {
				add(s)
			}
		}, func(dnssd.BrowseEntry) {})
		if err != nil && bctx.Err() == nil {
			multicastErr = err
		}
	}()
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(order) == 0 && unicastErr != nil && multicastErr != nil {
		return nil, fmt.Errorf("unicast: %v; multicast: %v", unicastErr, multicastErr)
	}
	out := make([]EmberServer, 0, len(order))
	for _, n := range order {
		out = append(out, seen[n])
	}
	return out, nil
}

func emberServerFrom(e dnssd.BrowseEntry) (EmberServer, bool) {
	if e.Port == 0 {
		return EmberServer{}, false
	}
	url := baseURLFor(e.IPs, e.Port)
	if url == "" {
		return EmberServer{}, false
	}
	return EmberServer{Name: e.Name, Host: e.Host, URL: url, Version: e.Text["version"]}, true
}

func legacyUnicastBrowse(ctx context.Context) ([]EmberServer, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	ifaces := multicastInterfaces()

	ids := map[uint16]bool{}
	send := func(name string, qtype uint16) {
		m := new(dns.Msg)
		m.SetQuestion(name, qtype)
		m.RecursionDesired = false
		ids[m.Id] = true
		b, err := m.Pack()
		if err != nil {
			return
		}
		if len(ifaces) == 0 {
			_, _ = conn.WriteToUDP(b, mdnsGroup)
			return
		}
		for i := range ifaces {
			if pc.SetMulticastInterface(&ifaces[i]) == nil {
				_, _ = conn.WriteToUDP(b, mdnsGroup)
			}
		}
	}

	send(emberBrowseName, dns.TypePTR)
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	return readEmberReplies(ctx, conn, deadline, ids, send)
}

func readEmberReplies(ctx context.Context, conn *net.UDPConn, deadline time.Time, ids map[uint16]bool, send func(name string, qtype uint16)) ([]EmberServer, error) {
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	resend := time.Now().Add(time.Second)
	var msgs []*dns.Msg
	askedA := map[string]bool{}
	buf := make([]byte, 9000)
	for {
		next := deadline
		if !resend.IsZero() && resend.Before(next) {
			next = resend
		}
		_ = conn.SetReadDeadline(next)
		if ctx.Err() != nil {
			break
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() == nil && !resend.IsZero() && time.Now().Before(deadline) {
					send(emberBrowseName, dns.TypePTR)
					resend = time.Time{}
					continue
				}
				break
			}
			return parseEmberAnswers(msgs), err
		}
		var m dns.Msg
		if m.Unpack(buf[:n]) != nil || !acceptReply(&m, ids) {
			continue
		}
		msgs = append(msgs, &m)
		for _, target := range srvTargetsWithoutA(msgs) {
			if !askedA[target] {
				askedA[target] = true
				send(target, dns.TypeA)
			}
		}
	}
	return parseEmberAnswers(msgs), nil
}

func acceptReply(m *dns.Msg, ids map[uint16]bool) bool {
	return m.Response && ids[m.Id]
}

func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

func srvTargetsWithoutA(msgs []*dns.Msg) []string {
	r := collectRecords(emberBrowseName, msgs)
	var out []string
	for _, inst := range r.instances {
		for _, s := range r.srv[strings.ToLower(inst)] {
			if len(r.ipv4s(s.Target)) == 0 {
				out = append(out, s.Target)
			}
		}
	}
	return out
}

func parseEmberAnswers(msgs []*dns.Msg) []EmberServer {
	r := collectRecords(emberBrowseName, msgs)
	var out []EmberServer
	seen := map[string]bool{}
	for _, inst := range r.instances {
		key := strings.ToLower(inst)
		version := ""
		for _, kv := range r.txt[key] {
			if v, ok := strings.CutPrefix(kv, "version="); ok {
				version = v
			}
		}
		for _, s := range r.srv[key] {
			if s.Port == 0 {
				continue
			}
			for _, ip := range r.ipv4s(s.Target) {
				es := EmberServer{
					Name:    instanceLabel(inst, emberBrowseName),
					Host:    s.Target,
					URL:     fmt.Sprintf("http://%s", net.JoinHostPort(ip.String(), fmt.Sprint(s.Port))),
					Version: version,
				}
				if k := es.Name + "\x00" + es.URL; !seen[k] {
					seen[k] = true
					out = append(out, es)
				}
			}
		}
	}
	return out
}
