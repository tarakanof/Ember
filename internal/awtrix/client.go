// Package awtrix is the HTTP client for the awtrix-ng firmware's API v1
// (https://blueforcer.github.io/awtrix-ng/reference/http/). It is the single
// place in Ember that knows NG endpoint paths and the NG error envelope.
//
// NG validates strictly: unknown payload keys are rejected with 422 and the
// offending field name, and PUT/PATCH without Content-Type: application/json
// are rejected with 415. Errors surface as *APIError so callers can log the
// field.
package awtrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one awtrix-ng device. Construct per call site with the
// currently-resolved base URL; it holds no connection state beyond the
// underlying http.Client.
type Client struct {
	base string
	hc   *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) BaseURL() string { return c.base }

// APIError is a non-2xx response, carrying the NG error envelope
// ({"error":{"code","message","field"}}) when the device supplied one.
type APIError struct {
	StatusCode int
	Code       string // e.g. "validationFailed"; empty if no envelope
	Message    string
	Field      string // offending payload key on validation errors
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "awtrix http %d", e.StatusCode)
	if e.Code != "" {
		b.WriteString(": " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Field != "" {
		b.WriteString(" (field " + e.Field + ")")
	}
	return b.String()
}

// AppInfo is one entry of GET /api/v1/apps. Origin is "builtin", "pushed", or
// "script" — "pushed" identifies apps Ember (or another API client) pushed.
type AppInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	InLoop  bool   `json:"inLoop"`
	Origin  string `json:"origin"`
}

// DeviceInfo is the subset of GET /api/v1/device Ember relies on.
type DeviceInfo struct {
	Version       string  `json:"version"`
	UID           string  `json:"uid"`
	BoardType     string  `json:"boardType"`
	Hostname      string  `json:"hostname"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	CurrentApp    string  `json:"currentApp"`
	BatteryPct    float64 `json:"batteryPercent"`
}

// PushApp creates or replaces a pushed app (PUT /api/v1/apps/pushed/{name}).
// Pushed apps are RAM-only in NG and vanish on reboot.
func (c *Client) PushApp(ctx context.Context, name string, payload map[string]any) error {
	return c.doJSON(ctx, http.MethodPut, "/api/v1/apps/pushed/"+url.PathEscape(name), payload, nil)
}

// DeleteApp removes a pushed app (DELETE /api/v1/apps/{name}). Replaces the
// AWTRIX3 empty-object POST, which NG rejects.
func (c *Client) DeleteApp(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/apps/"+url.PathEscape(name), nil, nil)
}

// ListApps returns every app on the device (GET /api/v1/apps), builtin and
// pushed alike.
func (c *Client) ListApps(ctx context.Context) ([]AppInfo, error) {
	var apps []AppInfo
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/apps", nil, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

func (c *Client) Notify(ctx context.Context, payload map[string]any) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/notifications", payload, nil)
}

// DismissNotify clears the currently-shown notification.
func (c *Client) DismissNotify(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/notifications/active", nil, nil)
}

// DismissNotifyByName clears the notification carrying name, wherever it sits
// in the queue (DELETE /api/v1/notifications/{name}; names are matched exactly).
// Unlike DismissNotify it can never clear a notification Ember did not push.
// A name the device does not hold answers 404, surfaced as *APIError.
func (c *Client) DismissNotifyByName(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("notification name is required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/notifications/"+url.PathEscape(name), nil, nil)
}

// Capabilities is GET /api/v1/capabilities: the name lists this firmware build
// supports, to be read rather than hardcoded. GPIO stays raw so a re-marshal
// reproduces the device's own JSON shape verbatim for pass-through consumers.
type Capabilities struct {
	Effects        []string        `json:"effects"`
	PaletteEffects []string        `json:"paletteEffects"`
	Transitions    []string        `json:"transitions"`
	Overlays       []string        `json:"overlays"`
	Palettes       []string        `json:"palettes"`
	Radio          bool            `json:"radio"`
	GPIO           json.RawMessage `json:"gpio,omitempty"`
}

// Capabilities fetches the firmware's supported name lists
// (GET /api/v1/capabilities).
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var caps Capabilities
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capabilities", nil, &caps)
	return caps, err
}

// PlayRTTTL plays an inline RTTTL melody (POST /api/v1/sounds/play).
func (c *Client) PlayRTTTL(ctx context.Context, rtttl string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/sounds/play", map[string]any{"rtttl": rtttl}, nil)
}

// PlaySound plays a melody file already on the device (/MELODIES) by name.
func (c *Client) PlaySound(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/sounds/play", map[string]any{"name": name}, nil)
}

func indicatorPath(index int) (string, error) {
	if index < 1 || index > 3 {
		return "", fmt.Errorf("indicator index must be 1-3, got %d", index)
	}
	return "/api/v1/indicators/" + strconv.Itoa(index), nil
}

// SetIndicator lights one of the three corner LEDs
// (PUT /api/v1/indicators/{1-3}; payload: color, blinkMs, fadeMs).
func (c *Client) SetIndicator(ctx context.Context, index int, payload map[string]any) error {
	p, err := indicatorPath(index)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPut, p, payload, nil)
}

// ClearIndicator turns a corner LED off (DELETE /api/v1/indicators/{1-3}).
func (c *Client) ClearIndicator(ctx context.Context, index int) error {
	p, err := indicatorPath(index)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, p, nil, nil)
}

// PatchSettings partially updates display settings (PATCH /api/v1/settings).
func (c *Client) PatchSettings(ctx context.Context, payload map[string]any) error {
	return c.doJSON(ctx, http.MethodPatch, "/api/v1/settings", payload, nil)
}

// SwitchApp forces the display to the named app (PUT /api/v1/apps/active).
func (c *Client) SwitchApp(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodPut, "/api/v1/apps/active", map[string]any{"name": name}, nil)
}

// ListIcons returns the filenames in /ICONS (GET /api/v1/files?dir=/ICONS).
func (c *Client) ListIcons(ctx context.Context) ([]string, error) {
	var out struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/files?dir="+url.QueryEscape("/ICONS"), nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Files))
	for _, f := range out.Files {
		names = append(names, f.Name)
	}
	return names, nil
}

// PutIcon uploads an icon into /ICONS (multipart POST /api/v1/files?dir=/ICONS).
// The device validates GIF/JPEG magic bytes.
func (c *Client) PutIcon(ctx context.Context, filename string, data []byte) error {
	if c.base == "" {
		return errors.New("awtrix base URL is required")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/v1/files?dir="+url.QueryEscape("/ICONS"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

// DeviceInfo fetches device identity/telemetry (GET /api/v1/device).
func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	var info DeviceInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/device", nil, &info)
	return info, err
}

func (c *Client) Reboot(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/device/reboot", nil, nil)
}

// doJSON performs one request. payload nil means no body; out nil means the
// response body is discarded after the status check.
func (c *Client) doJSON(ctx context.Context, method, path string, payload map[string]any, out any) error {
	if c.base == "" {
		return errors.New("awtrix base URL is required")
	}
	var rdr io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("awtrix %s: %w", path, err)
		}
	}
	return nil
}

// checkStatus maps non-2xx responses to *APIError, decoding the NG error
// envelope when present and falling back to the raw body otherwise.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	apiErr := &APIError{StatusCode: resp.StatusCode}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error.Code != "" {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
		apiErr.Field = envelope.Error.Field
	} else {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	return apiErr
}
