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
// supports, to be read rather than hardcoded. The typed fields are what Ember
// itself reads; the whole device document is kept in raw so a re-marshal
// reproduces it verbatim (audio, scriptUpdates, gpio and any key a later
// firmware adds), and pass-through consumers never lose a field.
type Capabilities struct {
	Effects        []string `json:"effects"`
	PaletteEffects []string `json:"paletteEffects"`
	Transitions    []string `json:"transitions"`
	Overlays       []string `json:"overlays"`
	Palettes       []string `json:"palettes"`
	// Audio lists the sound outputs the board has (NG 1.1.0 replaced the
	// top-level radio flag with this object).
	Audio         AudioCaps       `json:"audio"`
	ScriptUpdates bool            `json:"scriptUpdates"`
	GPIO          json.RawMessage `json:"gpio,omitempty"`

	raw json.RawMessage
}

// AudioCaps is capabilities.audio: which outputs answer POST /api/v1/audio/play.
// A key for an absent output answers 503 unavailable.
type AudioCaps struct {
	Buzzer bool `json:"buzzer"`
	Track  bool `json:"track"`
	MP3    bool `json:"mp3"`
	Radio  bool `json:"radio"`
}

// capabilitiesFields breaks the MarshalJSON/UnmarshalJSON recursion.
type capabilitiesFields Capabilities

func (c *Capabilities) UnmarshalJSON(b []byte) error {
	var f capabilitiesFields
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*c = Capabilities(f)
	c.raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON returns the device's own document when the value was decoded
// from one, and the typed fields otherwise.
func (c Capabilities) MarshalJSON() ([]byte, error) {
	if len(c.raw) > 0 {
		return c.raw, nil
	}
	return json.Marshal(capabilitiesFields(c))
}

// Capabilities fetches the firmware's supported name lists
// (GET /api/v1/capabilities).
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var caps Capabilities
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capabilities", nil, &caps)
	return caps, err
}

// PlayRTTTL plays an inline RTTTL melody on the buzzer
// (POST /api/v1/audio/play {"rtttl"}; NG 1.1.0 moved audio off /sounds/play).
// A board with no buzzer answers 503 unavailable.
func (c *Client) PlayRTTTL(ctx context.Context, rtttl string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/audio/play", map[string]any{"rtttl": rtttl}, nil)
}

// PlaySound plays a sound stored on the device by name
// (POST /api/v1/audio/play {"sound"}). The device picks the output: an MP3 of
// that name, then a melody, then a numbered DFPlayer track.
func (c *Client) PlaySound(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/audio/play", map[string]any{"sound": name}, nil)
}

// PlayMelody plays a melody stored on the device by name
// (POST /api/v1/audio/play {"melody"}). Unlike PlaySound it never falls back
// to an MP3 of the same name: an unknown name answers 404 notFound, a board
// with no buzzer 503 unavailable.
func (c *Client) PlayMelody(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/audio/play", map[string]any{"melody": name}, nil)
}

// StopAudio silences every output, radio included (POST /api/v1/audio/stop
// with no body, which is scope "all"). It works even while soundEnabled is off.
func (c *Client) StopAudio(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/audio/stop", nil, nil)
}

// Melody is one entry of GET /api/v1/audio/melodies. Notes and DurationMs come
// from parsing RTTTL and are 0 when it does not parse; a file that does not
// parse is still listed, with Valid false and the reason in Error at byte
// offset Index.
type Melody struct {
	Name       string `json:"name"`
	RTTTL      string `json:"rtttl"`
	Bytes      int    `json:"bytes"`
	Notes      int    `json:"notes"`
	DurationMs int    `json:"durationMs"`
	Valid      bool   `json:"valid"`
	Error      string `json:"error,omitempty"`
	Index      *int   `json:"index,omitempty"`
}

// MelodyList is GET /api/v1/audio/melodies. UsedBytes/TotalBytes cover the
// whole filesystem (icons, scripts and palettes share it), not just melodies.
type MelodyList struct {
	Melodies   []Melody `json:"melodies"`
	UsedBytes  int64    `json:"usedBytes"`
	TotalBytes int64    `json:"totalBytes"`
}

// Melodies lists the melody files stored on the device
// (GET /api/v1/audio/melodies). Melodies is never nil on success.
func (c *Client) Melodies(ctx context.Context) (MelodyList, error) {
	var out MelodyList
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/audio/melodies", nil, &out); err != nil {
		return MelodyList{}, err
	}
	if out.Melodies == nil {
		out.Melodies = []Melody{}
	}
	return out, nil
}

// SetDisplayPower blanks (false) or relights (true) the LED matrix
// (PATCH /api/v1/display {"power"}). The board, Wi-Fi and apps keep running;
// the state is runtime-only and a reboot relights the panel.
func (c *Client) SetDisplayPower(ctx context.Context, on bool) error {
	return c.doJSON(ctx, http.MethodPatch, "/api/v1/display", map[string]any{"power": on}, nil)
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

// GetSettings reads the full settings resource (GET /api/v1/settings).
func (c *Client) GetSettings(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/settings", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SwitchMode picks how SwitchApp moves the display to an app.
type SwitchMode int

const (
	// SwitchAnimated plays the device's configured transition.
	SwitchAnimated SwitchMode = iota
	// SwitchInstant jumps with no transition (NG's "fast":true).
	SwitchInstant
)

// SwitchApp forces the display to the named app (PUT /api/v1/apps/active).
func (c *Client) SwitchApp(ctx context.Context, name string, mode SwitchMode) error {
	body := map[string]any{"name": name}
	if mode == SwitchInstant {
		body["fast"] = true
	}
	return c.doJSON(ctx, http.MethodPut, "/api/v1/apps/active", body, nil)
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
	defer drainClose(resp.Body)
	return checkStatus(resp)
}

// DeviceInfo fetches device identity/telemetry (GET /api/v1/device).
func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	var info DeviceInfo
	err := c.doJSON(ctx, http.MethodGet, DevicePath, nil, &info)
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
	defer drainClose(resp.Body)
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

// drainClose reads what is left of a response body before closing it. Go's
// transport returns a connection to the keep-alive pool only when the body
// was read to EOF; closing early forces a fresh TCP handshake on the next
// request, which on the lossy Wi-Fi link to the clock is one more chance to
// lose a packet. The limit bounds the work on an unexpectedly large reply.
func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

// checkStatus maps non-2xx responses to *APIError, decoding the NG error
// envelope when present and falling back to the raw body otherwise.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return ParseAPIError(resp.StatusCode, raw)
}

// ParseAPIError builds the *APIError for a non-2xx device reply from its
// status and body: the NG envelope ({"error":{code,message,field}}) when the
// body carries one, the trimmed raw body as the message otherwise. Exported so
// the server's raw device proxy reports errors the same way this client does.
func ParseAPIError(status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Code != "" {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
		apiErr.Field = envelope.Error.Field
	} else {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	return apiErr
}
