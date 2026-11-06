package devices

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func fakeClaimKey(i int) string {
	k := make([]byte, ClaimKeyBytes)
	binary.BigEndian.PutUint32(k, uint32(i))
	return base64.RawURLEncoding.EncodeToString(k)
}

func TestClaimCapCountsAnIPv6SlashSixtyFourAsOneSource(t *testing.T) {
	cases := []struct {
		name   string
		first  []string
		capped string
		other  string
	}{
		{"one /64", []string{"2001:db8:1:2::1", "2001:db8:1:2:a:b:c:d"}, "2001:db8:1:2:ffff::9", "2001:db8:1:3::1"},
		{"link-local zones", []string{"fe80::1%en0", "fe80::2%en1"}, "fe80::3%en0", "2001:db8:1:3::1"},
		{"IPv4-mapped", []string{"192.0.2.10", "::ffff:192.0.2.10"}, "::ffff:c000:20a", "192.0.2.11"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := NewClaimRegistry()
			for i, ip := range c.first {
				if _, _, err := reg.Open(testHwID, "", ip, fakeClaimKey(i)); err != nil {
					t.Fatalf("open from %s: %v", ip, err)
				}
			}
			if _, _, err := reg.Open(testHwID, "", c.capped, fakeClaimKey(10)); !errors.Is(err, ErrClaimsFull) {
				t.Fatalf("third claim from the same source %s: err = %v, want errClaimsFull", c.capped, err)
			}
			if _, _, err := reg.Open(testHwID, "", c.other, fakeClaimKey(11)); err != nil {
				t.Fatalf("claim from another source %s: %v", c.other, err)
			}
		})
	}
}

func TestClaimCapsHoldUnderConcurrentOpens(t *testing.T) {
	reg := NewClaimRegistry()
	results := make(chan error, 200)
	for i := range 200 {
		go func() {
			ip := "192.0.2.1"
			if i%2 == 1 {
				ip = fmt.Sprintf("198.51.100.%d", i%100)
			}
			_, _, err := reg.Open(testHwID, "", ip, fakeClaimKey(i))
			results <- err
		}()
	}
	ok := 0
	for range 200 {
		if err := <-results; err == nil {
			ok++
		} else if !errors.Is(err, ErrClaimsFull) {
			t.Fatal(err)
		}
	}
	if ok != ClaimMaxTotal || len(reg.claims) != ClaimMaxTotal {
		t.Fatalf("opened %d claims (%d stored), want exactly %d", ok, len(reg.claims), ClaimMaxTotal)
	}
	fromOne := 0
	for _, c := range reg.claims {
		if c.IP == "192.0.2.1" {
			fromOne++
		}
	}
	if fromOne > claimMaxPerIP {
		t.Fatalf("one IP holds %d claims, want at most %d", fromOne, claimMaxPerIP)
	}
}
