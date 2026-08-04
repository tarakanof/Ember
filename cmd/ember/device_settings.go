package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// settingKind classifies how a device setting value is validated before it is
// forwarded to the clock's /api/settings.
type settingKind int

const (
	kBool settingKind = iota
	kInt
	kColor
	kEnum
	kString
)

type settingRule struct {
	kind     settingKind
	min, max int
	enum     map[string]bool
	maxLen   int
}

// overlayValues are the AWTRIX overlay effects we expose.
var overlayValues = map[string]bool{
	"clear": true, "snow": true, "rain": true, "drizzle": true,
	"storm": true, "thunder": true, "frost": true,
}

// deviceSettingRules is the whitelist of AWTRIX /api/settings keys the menu may
// write, with per-key validation. Keys absent here are rejected (never
// forwarded), so the proxy can't be used to poke arbitrary firmware settings.
var deviceSettingRules = map[string]settingRule{
	// General
	"BRI":       {kind: kInt, min: 0, max: 255},
	"ABRI":      {kind: kBool},
	"VOL":       {kind: kInt, min: 0, max: 30},
	"ATIME":     {kind: kInt, min: 1, max: 3600},
	"ATRANS":    {kind: kBool},
	"TEFF":      {kind: kInt, min: 0, max: 10},
	"TSPEED":    {kind: kInt, min: 0, max: 5000},
	"SSPEED":    {kind: kInt, min: 1, max: 1000},
	"TCOL":      {kind: kColor},
	"UPPERCASE": {kind: kBool},
	"BLOCKN":    {kind: kBool},
	"OVERLAY":   {kind: kEnum, enum: overlayValues},
	// Native apps
	"TIM":  {kind: kBool},
	"DAT":  {kind: kBool},
	"TEMP": {kind: kBool},
	"HUM":  {kind: kBool},
	"BAT":  {kind: kBool},
	// Time & Date
	"TFORMAT": {kind: kString, maxLen: 16},
	"DFORMAT": {kind: kString, maxLen: 16},
	"SOM":     {kind: kBool},
	"TMODE":   {kind: kInt, min: 0, max: 6},
	"CHCOL":   {kind: kColor},
	"CBCOL":   {kind: kColor},
	"CTCOL":   {kind: kColor},
	"WD":      {kind: kBool},
	"WDCA":    {kind: kColor},
	"WDCI":    {kind: kColor},
}

var hexColor = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)
var printableASCII = regexp.MustCompile(`^[\x20-\x7E]*$`)

// validateDeviceSettings rejects unknown keys and out-of-range / wrong-type
// values. A nil/empty map is valid (no-op write).
func validateDeviceSettings(m map[string]any) error {
	for k, v := range m {
		rule, ok := deviceSettingRules[k]
		if !ok {
			return fmt.Errorf("unknown setting %q", k)
		}
		switch rule.kind {
		case kBool:
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be a boolean", k)
			}
		case kInt:
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) || int(f) < rule.min || int(f) > rule.max {
				return fmt.Errorf("%s must be an integer in [%d,%d]", k, rule.min, rule.max)
			}
		case kColor:
			if !validColor(v) {
				return fmt.Errorf("%s must be a hex string or [r,g,b]", k)
			}
		case kEnum:
			s, ok := v.(string)
			if !ok || !rule.enum[s] {
				return fmt.Errorf("%s has an unsupported value", k)
			}
		case kString:
			s, ok := v.(string)
			if !ok || len(s) > rule.maxLen || !printableASCII.MatchString(s) {
				return fmt.Errorf("%s must be printable ASCII up to %d chars", k, rule.maxLen)
			}
		}
	}
	return nil
}

func validColor(v any) bool {
	if s, ok := v.(string); ok {
		return hexColor.MatchString(s)
	}
	if arr, ok := v.([]any); ok && len(arr) == 3 {
		for _, e := range arr {
			f, ok := e.(float64)
			if !ok || f < 0 || f > 255 || f != float64(int(f)) {
				return false
			}
		}
		return true
	}
	return false
}

// deviceBaseClient mirrors HTTPPublisher.baseAndClient but reads the live config
// directly, so device-settings proxying follows the same resolved clock URL.
func (a *App) deviceBaseClient() (string, *http.Client, error) {
	base := strings.TrimRight(a.cfg.Load().AWTRIX.HTTPBaseURL, "/")
	if base == "" {
		return "", nil, fmt.Errorf("clock not configured")
	}
	return base, &http.Client{Timeout: 8 * time.Second}, nil
}

// proxyToDevice performs a request against the clock and returns the response
// body. method is GET or POST; body is nil for GET.
func (a *App) proxyToDevice(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	base, cl, err := a.deviceBaseClient()
	if err != nil {
		return nil, 0, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return out, resp.StatusCode, nil
}

func (a *App) handleDeviceSettingsGet(w http.ResponseWriter, r *http.Request) {
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/settings", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	var all map[string]any
	if err := json.Unmarshal(body, &all); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// Filter to the whitelisted keys so the menu only ever sees what it manages.
	out := map[string]any{}
	for k, v := range all {
		if _, ok := deviceSettingRules[k]; ok {
			out[k] = v
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleDeviceSettingsPut(w http.ResponseWriter, r *http.Request) {
	var m map[string]any
	if err := decodeJSON(w, r, &m, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateDeviceSettings(m); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload, _ := json.Marshal(m)
	_, status, err := a.proxyToDevice(r.Context(), http.MethodPatch, "/api/v1/settings", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleDeviceStats(w http.ResponseWriter, r *http.Request) {
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/device", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// handleDeviceScreen passes through the clock's live framebuffer
// (GET /api/v1/display/screen) so the menu app can mirror the display.
// awtrix-ng wraps the pixels: {"width":32,"height":8,"pixels":[256 ints]}
// (AWTRIX3 returned the bare 256-int array) — consumers must unwrap.
func (a *App) handleDeviceScreen(w http.ResponseWriter, r *http.Request) {
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/display/screen", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (a *App) handleDeviceReboot(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, http.MethodPost, "/api/v1/device/reboot")
}

// handleDeviceDismiss clears the currently-shown notification
// (DELETE /api/v1/notifications/active — no body).
func (a *App) handleDeviceDismiss(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, http.MethodDelete, "/api/v1/notifications/active")
}

// handleDeviceNextApp / handleDevicePrevApp advance the clock to the next or
// previous app in its rotation (POST /api/v1/apps/next, /api/v1/apps/previous).
func (a *App) handleDeviceNextApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, http.MethodPost, "/api/v1/apps/next")
}

func (a *App) handleDevicePrevApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, http.MethodPost, "/api/v1/apps/previous")
}

// proxyAction sends a bodiless request to a clock action endpoint and maps
// the result.
func (a *App) proxyAction(w http.ResponseWriter, r *http.Request, method, path string) {
	_, status, err := a.proxyToDevice(r.Context(), method, path, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.WriteHeader(http.StatusOK)
}
