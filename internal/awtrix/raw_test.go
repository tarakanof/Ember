package awtrix

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRawCalls pins each pass-through call's method, path, body and content
// type, and that the device's reply comes back verbatim whatever its status.
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
	c, _ := serve(t, http.StatusOK, strings.Repeat("x", rawReplyLimit+10))
	reply, err := c.RawScreen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Body) != rawReplyLimit {
		t.Fatalf("body = %d bytes, want the %d cap", len(reply.Body), rawReplyLimit)
	}
}

func TestRawPutScriptReplyIsCapped(t *testing.T) {
	c, _ := serve(t, http.StatusOK, strings.Repeat("x", scriptReplyLimit+10))
	reply, err := c.RawPutScript(context.Background(), "s", "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Body) != scriptReplyLimit {
		t.Fatalf("body = %d bytes, want the %d cap", len(reply.Body), scriptReplyLimit)
	}
}
