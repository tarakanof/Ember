package printer

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, url, password string) *Client {
	t.Helper()
	c, err := NewClient(url, Credentials{User: DefaultUser, Password: password}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDigestCorrectPasswordReadsStatus(t *testing.T) {
	s := newStubPrinter(t)
	s.set(func(s *stubPrinter) { s.status = statusJSON("PRINTING", &jobFields{id: 412, progress: 42}) })
	c := newTestClient(t, s.srv.URL, testPassword)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Printer.State != "PRINTING" || st.Job == nil || st.Job.ID != 412 {
		t.Fatalf("status = %+v", st)
	}
	if got := s.total(); got != 2 {
		t.Fatalf("first read took %d requests, want challenge + answer", got)
	}
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.total(); got != 3 {
		t.Fatalf("second read took %d requests in all, want the nonce reused (3)", got)
	}
}

func TestDigestStaleNonceRecoversWithOneRetry(t *testing.T) {
	s := newStubPrinter(t)
	c := newTestClient(t, s.srv.URL, testPassword)
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := s.total()
	s.expireNonces()
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatalf("stale nonce: %v", err)
	}
	if got := s.total() - before; got != 2 {
		t.Fatalf("stale recovery took %d requests, want 2", got)
	}
	if s.staleSent != 1 {
		t.Fatalf("stale challenges = %d, want 1", s.staleSent)
	}
}

func TestDigestWrongPasswordIsReportedWithoutRetryStorm(t *testing.T) {
	s := newStubPrinter(t)
	c := newTestClient(t, s.srv.URL, "not-the-password")
	_, err := c.Status(context.Background())
	if !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("err = %v, want ErrWrongPassword", err)
	}
	if got := s.total(); got != 2 {
		t.Fatalf("wrong password took %d requests, want 2", got)
	}
}

func TestClientRefusesRedirects(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.Add(1)
	}))
	t.Cleanup(elsewhere.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/v1/status", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv.URL, testPassword)
	_, err := c.Status(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusFound {
		t.Fatalf("err = %v, want HTTP 302", err)
	}
	if other.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestClientRejectsPathsThatLeaveThePrinter(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:1", testPassword)
	for _, ref := range []string{"//evil.example/thumb/l/x", "/thumb/@evil.example/x", "http://evil.example/thumb/l/x"} {
		if _, err := c.Thumbnail(context.Background(), ref); err == nil {
			t.Errorf("Thumbnail(%q) succeeded", ref)
		}
	}
}

func TestThumbnailChecksTypeSizeAndMagic(t *testing.T) {
	png := testPNG(t, 1)
	huge := pngOfSize(t, MaxThumbSidePx+1, 1)
	cases := []struct {
		name    string
		typ     string
		body    []byte
		wantErr bool
		noThumb bool
	}{
		{"png", "image/png", png, false, false},
		{"png with params", "image/png; charset=binary", png, false, false},
		{"wrong type", "image/jpeg", png, true, false},
		{"not png bytes", "image/png", []byte("GIF89a......"), true, false},
		{"png magic only", "image/png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...), true, false},
		{"wider than 2048 px", "image/png", huge, true, false},
		{"over a megabyte", "image/png", append(png, make([]byte, MaxThumbBytes)...), true, false},
		{"none", "image/png", nil, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStubPrinter(t)
			s.set(func(s *stubPrinter) { s.thumb, s.thumbType = c.body, c.typ })
			cl := newTestClient(t, s.srv.URL, testPassword)
			b, err := cl.Thumbnail(context.Background(), "/thumb/l/usb/BRACKE~1.BGC")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.noThumb && !errors.Is(err, ErrNoThumbnail) {
				t.Fatalf("err = %v, want ErrNoThumbnail", err)
			}
			if err == nil && !bytes.Equal(b, c.body) {
				t.Fatal("body mismatch")
			}
		})
	}
}

func TestParseChallenge(t *testing.T) {
	c, ok := parseChallenge(`Digest realm="Printer API", nonce="0123456789abcdef", stale=true`)
	if !ok || c.realm != "Printer API" || c.nonce != "0123456789abcdef" || !c.stale {
		t.Fatalf("challenge = %+v ok=%v", c, ok)
	}
	c, ok = parseChallenge(`Digest nonce="ab\"c", realm="r,x", stale=FALSE`)
	if !ok || c.nonce != `ab"c` || c.realm != "r,x" || c.stale {
		t.Fatalf("challenge = %+v ok=%v", c, ok)
	}
	for _, h := range []string{"", `Basic realm="x"`, `Digest realm="x"`} {
		if _, ok := parseChallenge(h); ok {
			t.Errorf("parseChallenge(%q) ok", h)
		}
	}
}

func TestDigestResponseMatchesRFC2069(t *testing.T) {
	got := digestResponse("maker", "secret", "Printer API", "0123456789abcdef", "GET", "/api/v1/status")
	ha1 := md5Of("maker:Printer API:secret")
	ha2 := md5Of("GET:/api/v1/status")
	if want := md5Of(ha1 + ":0123456789abcdef:" + ha2); got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}
	h := authorization("maker", "secret", challenge{realm: "Printer API", nonce: "n"}, "GET", "/x")
	if strings.Contains(h, "secret") || strings.Contains(h, "qop") {
		t.Fatalf("header = %s", h)
	}
}
