package discovery

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brutella/dnssd"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func processCPU() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func TestLookupTypeLeavesNoSpinningReadersAfterTimeout(t *testing.T) {
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		err := lookupType(ctx, "_ember-leak-test._tcp.local.", func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
		cancel()
		if err != nil && time.Since(start) < 50*time.Millisecond {
			t.Skipf("no multicast socket here: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	before := processCPU()
	time.Sleep(time.Second)
	if burn := processCPU() - before; burn > 300*time.Millisecond {
		t.Fatalf("idle process burned %v CPU in 1s after timed-out lookups", burn)
	}
}

type localSocket struct {
	sock mdnsSocket
	addr *net.UDPAddr
	sink *net.UDPConn
}

func newLocalSocket4(t *testing.T) localSocket {
	t.Helper()
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetControlMessage(ipv4.FlagInterface, true)
	return localSocket{sock: socket4{pc: pc, dst: sink.LocalAddr().(*net.UDPAddr)}, addr: conn.LocalAddr().(*net.UDPAddr), sink: sink}
}

func newLocalSocket6(t *testing.T) localSocket {
	t.Helper()
	sink, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	pc := ipv6.NewPacketConn(conn)
	_ = pc.SetControlMessage(ipv6.FlagInterface, true)
	return localSocket{sock: socket6{pc: pc, dst: sink.LocalAddr().(*net.UDPAddr)}, addr: conn.LocalAddr().(*net.UDPAddr), sink: sink}
}

func announcement(t *testing.T, rrs ...string) []byte {
	t.Helper()
	m := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, Authoritative: true}}
	for _, s := range rrs {
		m.Answer = append(m.Answer, mustRR(t, s))
	}
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sendTo(t *testing.T, to *net.UDPAddr, b []byte) {
	t.Helper()
	network := "udp4"
	if to.IP.To4() == nil {
		network = "udp6"
	}
	c, err := net.DialUDP(network, nil, to)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write(b); err != nil {
		t.Fatal(err)
	}
}

func browseLocal(ctx context.Context, service string, ls localSocket) (<-chan dnssd.BrowseEntry, <-chan error) {
	entries := make(chan dnssd.BrowseEntry, 16)
	done := make(chan error, 1)
	go func() {
		done <- browseSockets(ctx, service, []mdnsSocket{ls.sock}, nil, func(e dnssd.BrowseEntry) { entries <- e }, func(dnssd.BrowseEntry) {})
	}()
	return entries, done
}

func nextEntry(t *testing.T, entries <-chan dnssd.BrowseEntry) dnssd.BrowseEntry {
	t.Helper()
	select {
	case e := <-entries:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no entry reported")
		return dnssd.BrowseEntry{}
	}
}

func lookupGoroutines() []string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(g, "testing.tRunner") {
			continue
		}
		if strings.Contains(g, "tarakanof/ember/internal/discovery.") || strings.Contains(g, "brutella/dnssd.") {
			out = append(out, g)
		}
	}
	return out
}

func TestBrowseSocketsLeavesNoGoroutinesWhileAnswersArrive(t *testing.T) {
	before := len(lookupGoroutines())
	flood := announcement(t,
		`_flood._tcp.local. 4500 IN PTR Flood._flood._tcp.local.`,
		`Flood._flood._tcp.local. 120 IN SRV 0 0 80 flood.local.`,
		`flood.local. 120 IN A 10.0.0.9`,
	)
	for i := range 40 {
		ls := newLocalSocket4(t)
		stopFlood := make(chan struct{})
		var flooders sync.WaitGroup
		flooders.Go(func() {
			c, err := net.DialUDP("udp4", nil, ls.addr)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				select {
				case <-stopFlood:
					return
				default:
					_, _ = c.Write(flood)
				}
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if i%2 == 1 {
			time.AfterFunc(10*time.Millisecond, cancel)
		}
		returned := make(chan struct{})
		go func() {
			defer close(returned)
			_ = browseSockets(ctx, "_flood._tcp.local.", []mdnsSocket{ls.sock}, nil, func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
		}()
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatalf("lookup %d did not return 1s after its context ended", i)
		}
		cancel()
		close(stopFlood)
		flooders.Wait()
	}
	var left []string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if left = lookupGoroutines(); len(left) <= before {
			return
		}
	}
	t.Fatalf("%d lookup goroutines left after 40 lookups (had %d):\n%s", len(left), before, strings.Join(left, "\n\n"))
}

func TestBrowseSocketsReportsAnnouncedServiceOverIPv4(t *testing.T) {
	ls := newLocalSocket4(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, _ := browseLocal(ctx, "_prusalink._tcp.local.", ls)
	sendTo(t, ls.addr, announcement(t,
		`_prusalink._tcp.local. 4500 IN PTR Prusa\ CORE\ One._prusalink._tcp.local.`,
		`Prusa\ CORE\ One._prusalink._tcp.local. 120 IN SRV 0 0 80 prusa-core-one.local.`,
		`Prusa\ CORE\ One._prusalink._tcp.local. 4500 IN TXT "path=/" "model=COREONE"`,
		`prusa-core-one.local. 120 IN A 192.168.0.50`,
		`prusa-core-one.local. 120 IN AAAA fe80::1`,
	))
	e := nextEntry(t, entries)
	if e.Name != "Prusa CORE One" || e.Host != "prusa-core-one" || e.Port != 80 || e.Text["model"] != "COREONE" || e.IfaceName == "" {
		t.Fatalf("entry = %+v", e)
	}
	if len(e.IPs) != 2 || !e.IPs[0].Equal(net.ParseIP("192.168.0.50")) || !e.IPs[1].Equal(net.ParseIP("fe80::1")) {
		t.Fatalf("IPs = %v", e.IPs)
	}
	if pl, ok := prusaLinkOf(e); !ok || pl.URL != "http://192.168.0.50:80" {
		t.Fatalf("prusaLinkOf = %+v, %v", pl, ok)
	}
}

func TestBrowseSocketsReportsAnnouncedServiceOverIPv6(t *testing.T) {
	ls := newLocalSocket6(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, _ := browseLocal(ctx, emberBrowseName, ls)
	sendTo(t, ls.addr, announcement(t,
		`_ember._tcp.local. 4500 IN PTR Ember._ember._tcp.local.`,
		`Ember._ember._tcp.local. 120 IN SRV 0 0 3627 nas.local.`,
		`Ember._ember._tcp.local. 4500 IN TXT "version=1.4.0"`,
		`nas.local. 120 IN AAAA 2001:db8::5`,
	))
	e := nextEntry(t, entries)
	got, ok := emberServerFrom(e)
	want := EmberServer{Name: "Ember", Host: "nas", URL: "http://[2001:db8::5]:3627", Version: "1.4.0"}
	if !ok || got != want {
		t.Fatalf("emberServerFrom(%+v) = %+v, %v; want %+v", e, got, ok, want)
	}
}

func TestBrowseSocketsIgnoresOtherServicesAndQueries(t *testing.T) {
	ls := newLocalSocket4(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, _ := browseLocal(ctx, awtrixServiceType, ls)
	sendTo(t, ls.addr, announcement(t,
		`_ember._tcp.local. 4500 IN PTR Ember._ember._tcp.local.`,
		`Ember._ember._tcp.local. 120 IN SRV 0 0 3627 nas.local.`,
		`nas.local. 120 IN A 10.0.0.5`,
	))
	known := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `Clock._awtrixng._tcp.local. 120 IN SRV 0 0 80 clock.local.`),
		mustRR(t, `clock.local. 120 IN A 10.0.0.9`),
	}}
	known.SetQuestion(awtrixServiceType, dns.TypePTR)
	b, err := known.Pack()
	if err != nil {
		t.Fatal(err)
	}
	sendTo(t, ls.addr, b)
	sendTo(t, ls.addr, announcement(t,
		`Clock._awtrixng._tcp.local. 120 IN SRV 0 0 80 clock.local.`,
		`clock.local. 120 IN A 10.0.0.10`,
	))
	if e := nextEntry(t, entries); e.Name != "Clock" || !e.IPs[0].Equal(net.ParseIP("10.0.0.10")) {
		t.Fatalf("first entry = %+v, want the announced clock only", e)
	}
}

func TestBrowseSocketsReportsServiceAgainAfterGoodbye(t *testing.T) {
	ls := newLocalSocket4(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, _ := browseLocal(ctx, prusaLinkServiceType, ls)
	hello := announcement(t,
		`_prusalink._tcp.local. 4500 IN PTR MK4._prusalink._tcp.local.`,
		`MK4._prusalink._tcp.local. 120 IN SRV 0 0 80 mk4.local.`,
		`mk4.local. 120 IN A 192.168.0.51`,
	)
	sendTo(t, ls.addr, hello)
	nextEntry(t, entries)
	sendTo(t, ls.addr, hello)
	sendTo(t, ls.addr, announcement(t,
		`_prusalink._tcp.local. 0 IN PTR MK4._prusalink._tcp.local.`,
		`MK4._prusalink._tcp.local. 0 IN SRV 0 0 80 mk4.local.`,
	))
	sendTo(t, ls.addr, hello)
	if e := nextEntry(t, entries); e.Name != "MK4" {
		t.Fatalf("entry after goodbye = %+v", e)
	}
	select {
	case e := <-entries:
		t.Fatalf("repeated announcement reported again: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBrowseSocketsSendsPTRQueryOnEverySocket(t *testing.T) {
	v4, v6 := newLocalSocket4(t), newLocalSocket6(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = browseSockets(ctx, prusaLinkServiceType, []mdnsSocket{v4.sock, v6.sock}, nil, func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
	for _, ls := range []localSocket{v4, v6} {
		_ = ls.sink.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 512)
		n, err := ls.sink.Read(buf)
		if err != nil {
			t.Fatalf("no query on %v: %v", ls.addr, err)
		}
		var m dns.Msg
		if err := m.Unpack(buf[:n]); err != nil || m.Response || len(m.Question) != 1 ||
			m.Question[0].Name != prusaLinkServiceType || m.Question[0].Qtype != dns.TypePTR {
			t.Fatalf("query = %v (%v)", m, err)
		}
	}
}

func TestWatchPrusaLinkReportsPrinterAnnouncedWhileWatching(t *testing.T) {
	ls := newLocalSocket4(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lookup := func(ctx context.Context, service string, add dnssd.AddFunc, rmv dnssd.RmvFunc) error {
		return browseSockets(ctx, service, []mdnsSocket{ls.sock}, nil, add, rmv)
	}
	seen := make(chan PrusaLink, 4)
	done := make(chan error, 1)
	go func() { done <- watchPrusaLink(ctx, lookup, func(p PrusaLink) { seen <- p }) }()
	time.Sleep(50 * time.Millisecond)
	sendTo(t, ls.addr, announcement(t,
		`_prusalink._tcp.local. 4500 IN PTR Prusa\ MK4._prusalink._tcp.local.`,
		`Prusa\ MK4._prusalink._tcp.local. 120 IN SRV 0 0 80 prusa-mk4.local.`,
		`prusa-mk4.local. 120 IN A 192.168.0.51`,
	))
	select {
	case p := <-seen:
		if want := (PrusaLink{Name: "Prusa MK4", Host: "prusa-mk4", URL: "http://192.168.0.51:80"}); p != want {
			t.Fatalf("seen %+v, want %+v", p, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("announced printer not reported")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch ended with %v, want nil", err)
	}
}

func TestMDNSRecordsKeepIPsPerInterface(t *testing.T) {
	r := newMDNSRecords(prusaLinkServiceType)
	srv := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `MK4._prusalink._tcp.local. 120 IN SRV 0 0 80 mk4.local.`),
		mustRR(t, `mk4.local. 120 IN A 192.168.0.51`),
	}}
	r.add(srv, "en0")
	r.add(&dns.Msg{Answer: []dns.RR{mustRR(t, `mk4.local. 120 IN A 10.8.0.51`)}}, "utun3")
	r.add(srv, "en0")
	got := r.entries()
	ifaces := []string{}
	for _, e := range got {
		ifaces = append(ifaces, e.IfaceName)
		if len(e.IPs) != 1 {
			t.Fatalf("entry %+v has %d IPs, want 1", e, len(e.IPs))
		}
	}
	if !slices.Equal(ifaces, []string{"en0", "utun3"}) {
		t.Fatalf("interfaces = %v", ifaces)
	}
}

func TestMDNSRecordsStayBoundedUnderAFlood(t *testing.T) {
	r := newMDNSRecords(prusaLinkServiceType)
	for i := range 1000 {
		r.add(&dns.Msg{Answer: []dns.RR{
			mustRR(t, fmt.Sprintf(`P%d._prusalink._tcp.local. 120 IN SRV 0 0 80 p.local.`, i)),
			mustRR(t, fmt.Sprintf(`P0._prusalink._tcp.local. 120 IN SRV 0 0 %d p.local.`, 1000+i)),
			mustRR(t, fmt.Sprintf(`p.local. 120 IN A 10.0.%d.%d`, i/250, i%250)),
		}}, "en0")
	}
	if len(r.instances) != maxMDNSInstances || len(r.srv["p0._prusalink._tcp.local."]) != maxSRVPerName || len(r.addrs["p.local."]) != maxAddrsPerHost {
		t.Fatalf("instances %d, SRVs for P0 %d, addresses %d", len(r.instances), len(r.srv["p0._prusalink._tcp.local."]), len(r.addrs["p.local."]))
	}
	r.add(&dns.Msg{Answer: []dns.RR{mustRR(t, `_prusalink._tcp.local. 0 IN PTR P1._prusalink._tcp.local.`)}}, "en0")
	r.add(&dns.Msg{Answer: []dns.RR{mustRR(t, `Late._prusalink._tcp.local. 120 IN SRV 0 0 80 late.local.`)}}, "en0")
	if !r.known["late._prusalink._tcp.local."] {
		t.Fatal("a goodbye did not free a slot for a new instance")
	}
}
