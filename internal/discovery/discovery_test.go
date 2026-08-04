package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ngDeviceServer serves GET /api/v1/device with the supplied body and 404s
// everything else, mimicking an awtrix-ng clock.
func ngDeviceServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeAcceptsNGDeviceRejectsOthers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"awtrix-ng", `{"version":"1.0.13","uid":"e868e705ffb8","boardType":"awtrixng","hostname":"Awtrix","uptimeSeconds":3874}`, true},
		{"wrong board type", `{"version":"1.0.13","uid":"e868e705ffb8","boardType":"esphome"}`, false},
		{"missing uid", `{"version":"1.0.13","boardType":"awtrixng"}`, false},
		{"not json", `<html>not awtrix</html>`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := ngDeviceServer(t, c.body)
			info, ok := probe(context.Background(), time.Second, srv.URL)
			if ok != c.want {
				t.Fatalf("probe ok=%v want %v (info=%+v)", ok, c.want, info)
			}
			if ok && (info.UID != "e868e705ffb8" || info.Version != "1.0.13") {
				t.Fatalf("probe info = %+v", info)
			}
		})
	}
}

func TestProbeRejectsNonAwtrixHTTPServer(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>hello</html>`))
	}))
	defer other.Close()
	if _, ok := probe(context.Background(), time.Second, other.URL); ok {
		t.Fatal("plain HTTP server should be rejected")
	}
}

func TestReachableUsesNGFingerprint(t *testing.T) {
	srv := ngDeviceServer(t, `{"version":"1.0.13","uid":"e868e705ffb8","boardType":"awtrixng"}`)
	ver, ok := Reachable(context.Background(), &http.Client{Timeout: time.Second}, srv.URL)
	if !ok || ver != "1.0.13" {
		t.Fatalf("Reachable = (%q, %v), want (\"1.0.13\", true)", ver, ok)
	}

	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/stats" {
			_, _ = w.Write([]byte(`{"uid":"awtrix_116ae8","version":"0.98"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer legacy.Close()
	if _, ok := Reachable(context.Background(), &http.Client{Timeout: time.Second}, legacy.URL); ok {
		t.Fatal("legacy AWTRIX3 /api/stats-only device must not fingerprint as awtrix-ng")
	}
}

func TestFilterCandidatesBuildsCandidate(t *testing.T) {
	srv := ngDeviceServer(t, `{"version":"1.0.13","uid":"u1","boardType":"awtrixng"}`)
	got := filterCandidates(context.Background(), time.Second,
		[]service{{host: "awtrixng-e705ffb8.local.", baseURL: srv.URL}})
	if len(got) != 1 || got[0].UID != "u1" || got[0].Version != "1.0.13" ||
		got[0].BaseURL != srv.URL || got[0].Host != "awtrixng-e705ffb8.local." {
		t.Fatalf("filterCandidates = %+v", got)
	}
}

func TestBaseURLForPrefersIPv4(t *testing.T) {
	ips := []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.0.16")}
	if got := baseURLFor(ips, 80); got != "http://192.168.0.16:80" {
		t.Fatalf("baseURLFor = %q", got)
	}
	if got := baseURLFor(nil, 80); got != "" {
		t.Fatalf("baseURLFor(nil) = %q want empty", got)
	}
}

func TestParseUDPReply(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantHost string
		wantPort int
	}{
		{"hostname only implies port 80", "Awtrix", "Awtrix", 80},
		{"hostname with port", "awtrixng-e705ffb8:8080", "awtrixng-e705ffb8", 8080},
		{"trailing whitespace trimmed", "Awtrix\n", "Awtrix", 80},
		{"empty body rejected", "", "", 0},
		{"non-numeric port rejected", "Awtrix:abc", "", 0},
		{"out-of-range port rejected", "Awtrix:99999", "", 0},
		{"empty hostname rejected", ":8080", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, port := parseUDPReply(c.body)
			if host != c.wantHost || port != c.wantPort {
				t.Fatalf("parseUDPReply(%q) = (%q, %d), want (%q, %d)", c.body, host, port, c.wantHost, c.wantPort)
			}
		})
	}
}

// TestCollectUDPRepliesUsesSourceIP drives the reply-collection half of the UDP
// fallback over real loopback UDP: a fake device answers with its hostname, and
// the collected service must carry the reply's SOURCE address (not the
// possibly-unresolvable hostname).
func TestCollectUDPRepliesUsesSourceIP(t *testing.T) {
	collector, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()

	device, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if _, err := device.WriteToUDP([]byte("Awtrix"), collector.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}

	got := collectUDPReplies(collector, time.Now().Add(500*time.Millisecond))
	wantBase := "http://" + net.JoinHostPort("127.0.0.1", "80")
	if len(got) != 1 || got[0].host != "Awtrix" || got[0].baseURL != wantBase {
		t.Fatalf("collectUDPReplies = %+v, want one {host:Awtrix baseURL:%s}", got, wantBase)
	}
}

// TestCollectUDPRepliesHonoursReplyPort covers the "<hostname>:<port>" form:
// the device's web port replaces the implied 80 in the base URL.
func TestCollectUDPRepliesHonoursReplyPort(t *testing.T) {
	collector, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	device, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if _, err := device.WriteToUDP([]byte("Awtrix:8080"), collector.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	got := collectUDPReplies(collector, time.Now().Add(500*time.Millisecond))
	if len(got) != 1 || got[0].baseURL != "http://127.0.0.1:8080" {
		t.Fatalf("collectUDPReplies = %+v, want baseURL http://127.0.0.1:8080", got)
	}
}
