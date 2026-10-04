package discovery

import (
	"net"
	"testing"

	"github.com/brutella/dnssd"
	"github.com/miekg/dns"
)

func TestEmberServerFromPrefersIPv4(t *testing.T) {
	e := dnssd.BrowseEntry{
		Name: "Ember", Host: "unraid.local.", Port: 3627,
		IPs:  []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.0.36")},
		Text: map[string]string{"version": "1.4.0", "path": "/healthz"},
	}
	got, ok := emberServerFrom(e)
	want := EmberServer{Name: "Ember", Host: "unraid.local.", URL: "http://192.168.0.36:3627", Version: "1.4.0"}
	if !ok || got != want {
		t.Fatalf("got %+v, %v", got, ok)
	}
}

func TestEmberServerFromSkipsUnresolved(t *testing.T) {
	if _, ok := emberServerFrom(dnssd.BrowseEntry{Name: "Ember", Port: 3627}); ok {
		t.Error("entry without IPs accepted")
	}
	if _, ok := emberServerFrom(dnssd.BrowseEntry{Name: "Ember", IPs: []net.IP{net.ParseIP("10.0.0.1")}}); ok {
		t.Error("entry without port accepted")
	}
}

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatalf("NewRR(%q): %v", s, err)
	}
	return rr
}

func TestParseEmberAnswersFromLegacyUnicastReply(t *testing.T) {
	// The shape the server's dnssd responder sends to a legacy unicast query.
	m := &dns.Msg{
		Answer: []dns.RR{mustRR(t, `_ember._tcp.local. 450 IN PTR Ember._ember._tcp.local.`)},
		Extra: []dns.RR{
			mustRR(t, `Ember._ember._tcp.local. 120 IN SRV 0 0 3627 Tosaka.local.`),
			mustRR(t, `Ember._ember._tcp.local. 450 IN TXT "path=/healthz" "version=1.4.0"`),
			mustRR(t, `Tosaka.local. 120 IN A 192.168.0.2`),
		},
	}
	got := parseEmberAnswers([]*dns.Msg{m})
	want := []EmberServer{{Name: "Ember", Host: "Tosaka.local.", URL: "http://192.168.0.2:3627", Version: "1.4.0"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseEmberAnswersJoinsSeparateAReply(t *testing.T) {
	ptr := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `_ember._tcp.local. 450 IN PTR Ember\ \(2\)._ember._tcp.local.`),
		mustRR(t, `Ember\ \(2\)._ember._tcp.local. 120 IN SRV 0 0 9000 nas.local.`),
	}}
	if got := srvTargetsWithoutA([]*dns.Msg{ptr}); len(got) != 1 || got[0] != "nas.local." {
		t.Fatalf("srvTargetsWithoutA = %q", got)
	}
	if got := parseEmberAnswers([]*dns.Msg{ptr}); len(got) != 0 {
		t.Fatalf("server without an address accepted: %+v", got)
	}
	a := &dns.Msg{Answer: []dns.RR{mustRR(t, `nas.local. 120 IN A 10.0.0.5`)}}
	got := parseEmberAnswers([]*dns.Msg{ptr, a})
	if len(got) != 1 || got[0].Name != "Ember (2)" || got[0].URL != "http://10.0.0.5:9000" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseEmberAnswersIgnoresOtherServices(t *testing.T) {
	m := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `_awtrixng._tcp.local. 450 IN PTR Awtrix._awtrixng._tcp.local.`),
		mustRR(t, `Awtrix._awtrixng._tcp.local. 120 IN SRV 0 0 80 awtrix.local.`),
		mustRR(t, `awtrix.local. 120 IN A 10.0.0.9`),
	}}
	if got := parseEmberAnswers([]*dns.Msg{m}); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestInstanceLabelUnescapes(t *testing.T) {
	for in, want := range map[string]string{
		"Ember._ember._tcp.local.":        "Ember",
		`Ember\ \(2\)._ember._tcp.local.`: "Ember (2)",
		`Ember\0322._ember._tcp.local.`:   "Ember 2",
	} {
		if got := instanceLabel(in); got != want {
			t.Errorf("instanceLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEmberAnswersKeepsConflictingAnswersForOneName(t *testing.T) {
	real := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `_ember._tcp.local. 450 IN PTR Ember._ember._tcp.local.`),
		mustRR(t, `Ember._ember._tcp.local. 120 IN SRV 0 0 3627 unraid.local.`),
		mustRR(t, `unraid.local. 120 IN A 192.168.0.36`),
	}}
	spoof := &dns.Msg{Answer: []dns.RR{
		mustRR(t, `_ember._tcp.local. 450 IN PTR Ember._ember._tcp.local.`),
		mustRR(t, `Ember._ember._tcp.local. 120 IN SRV 0 0 3627 evil.local.`),
		mustRR(t, `evil.local. 120 IN A 192.168.0.66`),
	}}
	got := parseEmberAnswers([]*dns.Msg{real, spoof})
	if len(got) != 2 || got[0].URL == got[1].URL {
		t.Fatalf("spoofed answer must surface as a second server, got %+v", got)
	}
}

func TestAcceptReplyNeedsResponseBitAndOurID(t *testing.T) {
	ids := map[uint16]bool{42: true}
	if !acceptReply(&dns.Msg{MsgHdr: dns.MsgHdr{Id: 42, Response: true}}, ids) {
		t.Error("valid reply rejected")
	}
	if acceptReply(&dns.Msg{MsgHdr: dns.MsgHdr{Id: 42}}, ids) {
		t.Error("query (QR unset) accepted")
	}
	if acceptReply(&dns.Msg{MsgHdr: dns.MsgHdr{Id: 7, Response: true}}, ids) {
		t.Error("reply to someone else's query accepted")
	}
}
