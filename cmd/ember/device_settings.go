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

	"github.com/tarakanof/ember/internal/awtrix"
)

// settingKind classifies how a device setting value is validated before it is
// forwarded to the clock's /api/v1/settings.
type settingKind int

const (
	kBool settingKind = iota
	kInt
	kColor
	kEnum
	kString
	kObject
	kNumber       // any finite float64, no bound (e.g. overlaySettings.speed)
	kStringOrNull // string up to maxLen, or JSON null (e.g. overlaySettings.palette)
	kColorOrNull  // a kColor, or JSON null to inherit (the per-app colours)
)

type settingRule struct {
	kind     settingKind
	min, max int
	enum     map[string]bool
	maxLen   int
	obj      map[string]settingRule // subkey rules, kObject only
}

func enumOf(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// scrollRules validates the nested "scroll" object. mode/direction/entry/
// whenFits are device-defined string enums (validated device-side, like
// transitionEffect below); speed/gap/holdMs are bounded ints.
var scrollRules = map[string]settingRule{
	"mode":      {kind: kString, maxLen: 32},
	"direction": {kind: kString, maxLen: 32},
	"entry":     {kind: kString, maxLen: 32},
	"whenFits":  {kind: kString, maxLen: 32},
	// speed is a percentage of the base scroll rate, not an absolute ms value.
	"speed":  {kind: kInt, min: 0, max: 1000},
	"gap":    {kind: kInt, min: 0, max: 64},
	"holdMs": {kind: kInt, min: 0, max: 60000},
}

// weekdayBarRules validates the nested "weekdayBar" object. Only the subkeys
// DeviceTab currently needs are whitelisted; the device also reports
// weekendDays/weekendActiveColor/weekendInactiveColor, deliberately left out
// (YAGNI — add them when a caller needs them).
var weekdayBarRules = map[string]settingRule{
	"show":          {kind: kBool},
	"startOnMonday": {kind: kBool},
	"activeColor":   {kind: kColor},
	"inactiveColor": {kind: kColor},
}

// deviceSettingRules is the whitelist of awtrix-ng /api/v1/settings keys the
// menu may write, with per-key validation. Keys absent here are rejected
// (never forwarded), so the proxy can't be used to poke arbitrary firmware
// settings.
var deviceSettingRules = map[string]settingRule{
	// General
	"brightness":     {kind: kInt, min: 0, max: 255},
	"autoBrightness": {kind: kBool},
	// Sound (NG 1.1.0): soundEnabled mutes the device; buzzerVolume replaced
	// the 0-30 "volume", which NG now rejects with 422. The TC001 has only the
	// piezo, so the DFPlayer/MP3/radio volumes are left out.
	"soundEnabled": {kind: kBool},
	"buzzerVolume": {kind: kInt, min: 0, max: 100},
	// appDurationMs is milliseconds on NG (was ATIME, seconds, 1-3600, on
	// AWTRIX3) — 1s-1h is a sane bound for a rotating app's dwell time.
	"appDurationMs":        {kind: kInt, min: 1000, max: 3600000},
	"autoTransition":       {kind: kBool}, // Pomodoro takeover key; coordinator.go writes this directly
	"transitionDurationMs": {kind: kInt, min: 0, max: 60000},
	// transitionEffect is a device-reported name (GET /api/v1/capabilities),
	// not a static enum — capabilities-fetch plumbing to validate the live set
	// is ticket #70. Here we only bound it to a plausible identifier shape;
	// an unknown name is rejected by the device itself with a 422.
	"transitionEffect": {kind: kString, maxLen: 32},
	"textColor":        {kind: kColor},
	"uppercase":        {kind: kBool},
	"blockNavigation":  {kind: kBool}, // Pomodoro takeover key; coordinator.go writes this directly
	// Time & Date — NG replaced the TFORMAT/DFORMAT strftime strings with
	// discrete typed fields; there are no format strings to validate anymore.
	"timeMode":            {kind: kInt, min: 0, max: 6},
	"time24h":             {kind: kBool},
	"timeLeadingZero":     {kind: kBool},
	"timeShowSeconds":     {kind: kBool},
	"timeShowAmPm":        {kind: kBool},
	"timeSeparatorMode":   {kind: kEnum, enum: enumOf("steady", "blink", "pulse")},
	"dateOrder":           {kind: kEnum, enum: enumOf("dayMonthYear", "monthDayYear", "yearMonthDay")},
	"dateSeparator":       {kind: kEnum, enum: enumOf("dot", "slash", "dash")},
	"dateYearMode":        {kind: kEnum, enum: enumOf("none", "twoDigit", "fourDigit")},
	"dateShowWeekday":     {kind: kBool},
	"dateMonthNames":      {kind: kBool},
	"calendarHeaderColor": {kind: kColor},
	"calendarBodyColor":   {kind: kColor},
	"calendarTextColor":   {kind: kColor},
	// Native Apps — per-builtin-app text color (null = inherit textColor, which
	// is how NG reports an unset one), plus an app-adjacent toggle (issue #92).
	// There is no smoothScroll: that was AWTRIX3's SSCROLL; scroll.mode is the
	// NG equivalent.
	"timeColor":        {kind: kColorOrNull},
	"dateColor":        {kind: kColorOrNull},
	"temperatureColor": {kind: kColorOrNull},
	"humidityColor":    {kind: kColorOrNull},
	"batteryColor":     {kind: kColorOrNull},
	"useCelsius":       {kind: kBool},
	// Nested objects — the device speaks these NG shapes directly; the macOS
	// app adapts to them in #71.
	"scroll":     {kind: kObject, obj: scrollRules},
	"weekdayBar": {kind: kObject, obj: weekdayBarRules},
}

var hexColor = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)
var printableASCII = regexp.MustCompile(`^[\x20-\x7E]*$`)

// validateDeviceSettings rejects unknown keys and out-of-range / wrong-type
// values. A nil/empty map is valid (no-op write).
func validateDeviceSettings(m map[string]any) error {
	return validateAgainstRules(m, deviceSettingRules)
}

// validateAgainstRules checks m against rules, recursing into kObject values.
func validateAgainstRules(m map[string]any, rules map[string]settingRule) error {
	for k, v := range m {
		rule, ok := rules[k]
		if !ok {
			return fmt.Errorf("unknown setting %q", k)
		}
		if err := validateSettingValue(k, v, rule); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingValue(k string, v any, rule settingRule) error {
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
	case kColorOrNull:
		if v != nil && !validColor(v) {
			return fmt.Errorf("%s must be null, a hex string or [r,g,b]", k)
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
	case kObject:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", k)
		}
		if err := validateAgainstRules(obj, rule.obj); err != nil {
			return fmt.Errorf("%s.%w", k, err)
		}
	case kNumber:
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s must be a number", k)
		}
	case kStringOrNull:
		if v == nil {
			return nil
		}
		s, ok := v.(string)
		if !ok || len(s) > rule.maxLen || !printableASCII.MatchString(s) {
			return fmt.Errorf("%s must be null or printable ASCII up to %d chars", k, rule.maxLen)
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

// deviceProxyStatus picks the status the menu sees for a non-2xx clock reply.
// Request errors (bad value, unknown key, missing app, wrong media type) and
// a busy/absent-hardware 503 pass through unchanged, so the caller can tell
// "the clock refused this" from "the clock is broken or unreachable".
// Everything else becomes 502; a device 401/403 in particular must not read
// as the menu's own bearer token being wrong.
func deviceProxyStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity, http.StatusServiceUnavailable:
		return status
	}
	return http.StatusBadGateway
}

// writeDeviceError relays a non-2xx clock reply to the menu. The NG envelope
// ({"error":{code,message,field}}) is flattened into the server's own error
// shape — "error" stays a string, which is what the menu displays — with
// "code" and "field" alongside so a caller can point at the rejected key.
func writeDeviceError(w http.ResponseWriter, status int, body []byte) {
	apiErr := awtrix.ParseAPIError(status, body)
	msg := fmt.Sprintf("clock returned %d", status)
	if detail := apiErr.Message; detail != "" {
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		msg += ": " + detail
	}
	if apiErr.Field != "" {
		msg += " (field " + apiErr.Field + ")"
	}
	out := map[string]string{"error": msg}
	if apiErr.Code != "" {
		out["code"] = apiErr.Code
	}
	if apiErr.Field != "" {
		out["field"] = apiErr.Field
	}
	writeJSON(w, deviceProxyStatus(status), out)
}

func (a *App) handleDeviceSettingsGet(w http.ResponseWriter, r *http.Request) {
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/settings", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeDeviceError(w, status, body)
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
	reply, status, err := a.proxyToDevice(r.Context(), http.MethodPatch, "/api/v1/settings", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeDeviceError(w, status, reply)
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
		writeDeviceError(w, status, body)
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
		writeDeviceError(w, status, body)
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
	reply, status, err := a.proxyToDevice(r.Context(), method, path, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeDeviceError(w, status, reply)
		return
	}
	w.WriteHeader(http.StatusOK)
}
