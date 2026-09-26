package discovery

import "testing"

func TestPortFromAddr(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want int
	}{
		{":3627", 3627},
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
