package discovery

import "testing"

func TestAdvertiseEnabledGate(t *testing.T) {
	cases := map[string]bool{
		"": true, "1": true, "true": true, "TRUE": true, "yes": true,
		"0": false, "false": false, "no": false, "off": false, " off ": false,
	}
	for in, want := range cases {
		if got := AdvertiseEnabled(in); got != want {
			t.Fatalf("AdvertiseEnabled(%q)=%v want %v", in, got, want)
		}
	}
}

func TestPortFromAddr(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want int
	}{
		{":8080", 8080},
		{"0.0.0.0:9000", 9000},
		{"127.0.0.1:80", 80},
	} {
		if p, err := PortFromAddr(tc.addr); err != nil || p != tc.want {
			t.Fatalf("PortFromAddr(%q)=%d,%v want %d", tc.addr, p, err, tc.want)
		}
	}
	if _, err := PortFromAddr("not-an-addr"); err == nil {
		t.Fatal("expected error for malformed addr")
	}
}
