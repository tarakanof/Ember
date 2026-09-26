package awtrix

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Reply is a device response kept verbatim, for the server's pass-through
// proxies (the menu's Device tab relays the clock's own JSON and status). A
// non-2xx status is a Reply, not an error; the error return is for requests
// that never got an answer.
type Reply struct {
	Status int
	Body   []byte
}

// rawReplyLimit caps how much of a pass-through reply is read. The largest NG
// document Ember relays is the 32×8 screen dump, well under this.
const rawReplyLimit = 1 << 20

// scriptReplyLimit caps a script upload's reply: it is a short JSON status,
// or a compiler message on a failed compile.
const scriptReplyLimit = 8 << 10

// DevicePath is the device identity/telemetry resource. Exported only so
// diagnostics can name the URL they probed.
const DevicePath = "/api/v1/device"

// RawDevice is GET /api/v1/device.
func (c *Client) RawDevice(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, DevicePath, nil)
}

// RawReboot is POST /api/v1/device/reboot.
func (c *Client) RawReboot(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/device/reboot", nil)
}

// RawSettings is GET /api/v1/settings.
func (c *Client) RawSettings(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/settings", nil)
}

// RawPatchSettings is PATCH /api/v1/settings with a JSON body.
func (c *Client) RawPatchSettings(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPatch, "/api/v1/settings", body)
}

// RawSystem is GET /api/v1/system: the whole system object, Wi-Fi
// credentials included, so callers must not relay it unfiltered.
func (c *Client) RawSystem(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/system", nil)
}

// RawPutSystem is PUT /api/v1/system. NG replaces the object, so body must be
// a full read-merge of RawSystem, never a partial one.
func (c *Client) RawPutSystem(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPut, "/api/v1/system", body)
}

// RawDisplay is GET /api/v1/display.
func (c *Client) RawDisplay(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/display", nil)
}

// RawPatchDisplay is PATCH /api/v1/display with a JSON body.
func (c *Client) RawPatchDisplay(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPatch, "/api/v1/display", body)
}

// RawScreen is GET /api/v1/display/screen, the live pixel dump.
func (c *Client) RawScreen(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/display/screen", nil)
}

// RawCapabilities is GET /api/v1/capabilities.
func (c *Client) RawCapabilities(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/capabilities", nil)
}

// RawApps is GET /api/v1/apps.
func (c *Client) RawApps(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/apps", nil)
}

// RawPutAppOrder is PUT /api/v1/apps/order with a JSON body.
func (c *Client) RawPutAppOrder(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPut, "/api/v1/apps/order", body)
}

// RawNextApp is POST /api/v1/apps/next.
func (c *Client) RawNextApp(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/apps/next", nil)
}

// RawPreviousApp is POST /api/v1/apps/previous.
func (c *Client) RawPreviousApp(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/apps/previous", nil)
}

// RawDeleteApp is DELETE /api/v1/apps/{name}. It removes any app, a Berry
// script included: to the firmware a script is an app.
func (c *Client) RawDeleteApp(ctx context.Context, name string) (Reply, error) {
	return c.raw(ctx, http.MethodDelete, "/api/v1/apps/"+url.PathEscape(name), nil)
}

// RawDismissNotify is DELETE /api/v1/notifications/active.
func (c *Client) RawDismissNotify(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodDelete, "/api/v1/notifications/active", nil)
}

// scriptPath is NG's Berry script resource: PUT installs or replaces raw
// source, GET serves it back. Removal goes through RawDeleteApp.
func scriptPath(name string) string { return "/api/v1/apps/script/" + url.PathEscape(name) }

// RawScript is GET /api/v1/apps/script/{name}: the Berry source as stored.
func (c *Client) RawScript(ctx context.Context, name string) (Reply, error) {
	return c.raw(ctx, http.MethodGet, scriptPath(name), nil)
}

// RawPutScript is PUT /api/v1/apps/script/{name} with the source as
// text/plain. A script that fails to compile still installs with a 200; the
// compiler message is in the reply body's "error".
func (c *Client) RawPutScript(ctx context.Context, name, source string) (Reply, error) {
	return c.do(ctx, http.MethodPut, scriptPath(name), strings.NewReader(source), "text/plain", scriptReplyLimit)
}

// raw sends body (nil for none) as JSON and returns the reply verbatim.
func (c *Client) raw(ctx context.Context, method, path string, body []byte) (Reply, error) {
	if body == nil {
		return c.do(ctx, method, path, nil, "", rawReplyLimit)
	}
	return c.do(ctx, method, path, bytes.NewReader(body), "application/json", rawReplyLimit)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, limit int64) (Reply, error) {
	if c.base == "" {
		return Reply{}, errors.New("awtrix base URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return Reply{}, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer drainClose(resp.Body)
	out, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return Reply{Status: resp.StatusCode, Body: out}, nil
}
