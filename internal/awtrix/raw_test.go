package awtrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRawCalls(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"k":1}`)
	cases := []struct {
		name        string
		call        func(*Client) (Reply, error)
		method      string
		path        string
		contentType string
		body        string
	}{
		{"device", func(c *Client) (Reply, error) { return c.RawDevice(ctx) }, "GET", "/api/v1/device", "", ""},
		{"reboot", func(c *Client) (Reply, error) { return c.RawReboot(ctx) }, "POST", "/api/v1/device/reboot", "", ""},
		{"settings", func(c *Client) (Reply, error) { return c.RawSettings(ctx) }, "GET", "/api/v1/settings", "", ""},
		{"patch settings", func(c *Client) (Reply, error) { return c.RawPatchSettings(ctx, body) }, "PATCH", "/api/v1/settings", "application/json", `{"k":1}`},
		{"system", func(c *Client) (Reply, error) { return c.RawSystem(ctx) }, "GET", "/api/v1/system", "", ""},
		{"put system", func(c *Client) (Reply, error) { return c.RawPutSystem(ctx, body) }, "PUT", "/api/v1/system", "application/json", `{"k":1}`},
		{"display", func(c *Client) (Reply, error) { return c.RawDisplay(ctx) }, "GET", "/api/v1/display", "", ""},
		{"patch display", func(c *Client) (Reply, error) { return c.RawPatchDisplay(ctx, body) }, "PATCH", "/api/v1/display", "application/json", `{"k":1}`},
		{"screen", func(c *Client) (Reply, error) { return c.RawScreen(ctx) }, "GET", "/api/v1/display/screen", "", ""},
		{"capabilities", func(c *Client) (Reply, error) { return c.RawCapabilities(ctx) }, "GET", "/api/v1/capabilities", "", ""},
		{"apps", func(c *Client) (Reply, error) { return c.RawApps(ctx) }, "GET", "/api/v1/apps", "", ""},
		{"app order", func(c *Client) (Reply, error) { return c.RawPutAppOrder(ctx, body) }, "PUT", "/api/v1/apps/order", "application/json", `{"k":1}`},
		{"app on", func(c *Client) (Reply, error) { return c.RawPutAppEnabled(ctx, "Battery", true) }, "PUT", "/api/v1/apps/Battery/enabled", "application/json", "true"},
		{"app off", func(c *Client) (Reply, error) { return c.RawPutAppEnabled(ctx, "Battery", false) }, "PUT", "/api/v1/apps/Battery/enabled", "application/json", "false"},
		{"next app", func(c *Client) (Reply, error) { return c.RawNextApp(ctx) }, "POST", "/api/v1/apps/next", "", ""},
		{"previous app", func(c *Client) (Reply, error) { return c.RawPreviousApp(ctx) }, "POST", "/api/v1/apps/previous", "", ""},
		{"delete app", func(c *Client) (Reply, error) { return c.RawDeleteApp(ctx, "ember-boot-ping") }, "DELETE", "/api/v1/apps/ember-boot-ping", "", ""},
		{"dismiss", func(c *Client) (Reply, error) { return c.RawDismissNotify(ctx) }, "DELETE", "/api/v1/notifications/active", "", ""},
		{"script", func(c *Client) (Reply, error) { return c.RawScript(ctx, "ember-boot-ping") }, "GET", "/api/v1/apps/script/ember-boot-ping", "", ""},
		{"put script", func(c *Client) (Reply, error) { return c.RawPutScript(ctx, "ember-boot-ping", "print(1)") }, "PUT", "/api/v1/apps/script/ember-boot-ping", "text/plain", "print(1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusUnprocessableEntity} {
				c, rec := serve(t, status, `{"reply":true}`)
				reply, err := tc.call(c)
				if err != nil {
					t.Fatalf("status %d: err = %v, want the reply", status, err)
				}
				if reply.Status != status || string(reply.Body) != `{"reply":true}` {
					t.Fatalf("reply = %d %q", reply.Status, reply.Body)
				}
				if rec.method != tc.method || rec.path != tc.path {
					t.Fatalf("request = %s %s, want %s %s", rec.method, rec.path, tc.method, tc.path)
				}
				if rec.contentType != tc.contentType || string(rec.body) != tc.body {
					t.Fatalf("sent %q body %q, want %q body %q", rec.contentType, rec.body, tc.contentType, tc.body)
				}
			}
		})
	}
}

func TestRawCallNeedsBaseURL(t *testing.T) {
	if _, err := NewClient("", time.Second).RawDevice(context.Background()); err == nil {
		t.Fatal("empty base URL: want an error")
	}
}

func TestRawReplyIsCapped(t *testing.T) {
	c, _ := serveWithTimeout(t, http.StatusOK, strings.Repeat("x", rawReplyLimit+10), time.Minute)
	reply, err := c.RawScreen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Body) != rawReplyLimit {
		t.Fatalf("body = %d bytes, want the %d cap", len(reply.Body), rawReplyLimit)
	}
}

func TestRawPutScriptReplyIsCapped(t *testing.T) {
	c, _ := serveWithTimeout(t, http.StatusOK, strings.Repeat("x", scriptReplyLimit+10), time.Minute)
	reply, err := c.RawPutScript(context.Background(), "s", "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Body) != scriptReplyLimit {
		t.Fatalf("body = %d bytes, want the %d cap", len(reply.Body), scriptReplyLimit)
	}
}

func serveCut(t *testing.T, status, declared int, sent string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\n\r\n%s", status, http.StatusText(status), declared, sent)
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, time.Minute)
}

func TestRawReplyCutMidStreamIsAnError(t *testing.T) {
	c := serveCut(t, http.StatusOK, 1000, `{"partial":`)
	reply, err := c.RawScreen(context.Background())
	if err == nil {
		t.Fatalf("cut body: reply = %d %q, want an error", reply.Status, reply.Body)
	}
}

func TestRawRejectionCutMidStreamKeepsItsStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := serveCut(t, status, 1000, `{"error":`)
			reply, err := c.RawScreen(context.Background())
			if err != nil {
				t.Fatalf("cut %d body: err = %v, want the reply", status, err)
			}
			if reply.Status != status || string(reply.Body) != `{"error":` {
				t.Fatalf("reply = %d %q, want %d with the partial body", reply.Status, reply.Body, status)
			}
		})
	}
}

type headersSeen struct {
	base http.RoundTripper
	seen chan struct{}
}

func (h headersSeen) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := h.base.RoundTrip(r)
	if err == nil {
		close(h.seen)
	}
	return resp, err
}

func TestRawReplyAbortedMidBodyIsAReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial":`))
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, time.Minute)
	seen := make(chan struct{})
	c.hc.Transport = headersSeen{base: http.DefaultTransport, seen: seen}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-seen
		cancel()
	}()
	reply, err := c.RawScreen(ctx)
	if err == nil {
		t.Fatalf("aborted body: reply = %d %q, want an error", reply.Status, reply.Body)
	}
	if !strings.HasPrefix(err.Error(), "read reply: ") || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the body read's context.Canceled", err)
	}
}

func TestRawReplyCutAfterTheCapIsNotAnError(t *testing.T) {
	c := serveCut(t, http.StatusOK, scriptReplyLimit+1000, strings.Repeat("x", scriptReplyLimit+10))
	reply, err := c.RawPutScript(context.Background(), "s", "src")
	if err != nil {
		t.Fatalf("cut past the cap: err = %v, want the capped reply", err)
	}
	if len(reply.Body) != scriptReplyLimit {
		t.Fatalf("body = %d bytes, want the %d cap", len(reply.Body), scriptReplyLimit)
	}
}
