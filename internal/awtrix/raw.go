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

type Reply struct {
	Status int
	Body   []byte
}

const rawReplyLimit = 1 << 20

const scriptReplyLimit = 8 << 10

const DevicePath = "/api/v1/device"

func (c *Client) RawDevice(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, DevicePath, nil)
}

func (c *Client) RawReboot(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/device/reboot", nil)
}

func (c *Client) RawSettings(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/settings", nil)
}

func (c *Client) RawPatchSettings(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPatch, "/api/v1/settings", body)
}

// RawSystem is GET /api/v1/system, Wi-Fi credentials included: never relay it unfiltered.
func (c *Client) RawSystem(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/system", nil)
}

func (c *Client) RawPutSystem(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPut, "/api/v1/system", body)
}

func (c *Client) RawDisplay(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/display", nil)
}

func (c *Client) RawPatchDisplay(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPatch, "/api/v1/display", body)
}

func (c *Client) RawScreen(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/display/screen", nil)
}

func (c *Client) RawCapabilities(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/capabilities", nil)
}

func (c *Client) RawApps(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/apps", nil)
}

func (c *Client) RawPutAppOrder(ctx context.Context, body []byte) (Reply, error) {
	return c.raw(ctx, http.MethodPut, "/api/v1/apps/order", body)
}

func (c *Client) RawNextApp(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/apps/next", nil)
}

func (c *Client) RawPreviousApp(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/apps/previous", nil)
}

func (c *Client) RawDeleteApp(ctx context.Context, name string) (Reply, error) {
	return c.raw(ctx, http.MethodDelete, "/api/v1/apps/"+url.PathEscape(name), nil)
}

func (c *Client) RawDismissNotify(ctx context.Context) (Reply, error) {
	return c.raw(ctx, http.MethodDelete, "/api/v1/notifications/active", nil)
}

func scriptPath(name string) string { return "/api/v1/apps/script/" + url.PathEscape(name) }

func (c *Client) RawScript(ctx context.Context, name string) (Reply, error) {
	return c.raw(ctx, http.MethodGet, scriptPath(name), nil)
}

func (c *Client) RawPutScript(ctx context.Context, name, source string) (Reply, error) {
	return c.do(ctx, http.MethodPut, scriptPath(name), strings.NewReader(source), "text/plain", scriptReplyLimit)
}

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
