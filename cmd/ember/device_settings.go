package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

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
	"autoTransition":       {kind: kBool}, // Pomodoro takeover key: held back while a takeover is in force (applyMenuSettings)
	"transitionDurationMs": {kind: kInt, min: 0, max: 60000},
	// transitionEffect is a device-reported name (GET /api/v1/capabilities),
	// not a static enum — capabilities-fetch plumbing to validate the live set
	// is ticket #70. Here we only bound it to a plausible identifier shape;
	// an unknown name is rejected by the device itself with a 422.
	"transitionEffect": {kind: kString, maxLen: 32},
	"textColor":        {kind: kColor},
	"uppercase":        {kind: kBool},
	"blockNavigation":  {kind: kBool}, // Pomodoro takeover key: held back while a takeover is in force (applyMenuSettings)
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

// deferredKeysHeader names, comma-separated, the takeover keys a
// /v1/device/settings response answered from the Pomodoro takeover snapshot
// rather than the device: on GET, the values reported are the user's own
// (what the clock returns to after the focus block); on PUT, the keys that
// were saved for then instead of written now. Absent when no takeover is in
// force. A header, not a body field, so the body stays pure NG settings keys.
const deferredKeysHeader = "X-Ember-Deferred-Keys"

func (a *App) handleDeviceSettingsGet(w http.ResponseWriter, r *http.Request) {
	before, hadBefore := a.coord.takeoverPriorView()
	body, err := a.clock.fetch(r.Context(), (*awtrix.Client).RawSettings)
	if err != nil {
		writeClockError(w, err)
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
	// Mid-focus the device holds the takeover's values; the menu shows the
	// user's own, so its toggles don't flip for the length of a focus block
	// and a save of an unrelated key can't write the takeover values back
	// as the user's choice. The snapshot is read both sides of the clock
	// read: a restore that completes during it leaves the device answer
	// with the takeover values and no snapshot after, so the one from
	// before (what the restore wrote) is used.
	p, ok := a.coord.takeoverPriorView()
	if !ok {
		p, ok = before, hadBefore
	}
	if ok {
		for k, v := range p.settings() {
			out[k] = v
		}
		w.Header().Set(deferredKeysHeader, strings.Join(takeoverKeys, ","))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleDeviceSettingsPut(w http.ResponseWriter, r *http.Request) {
	var m map[string]any
	if !a.decodeOrReject(w, r, &m, false) {
		return
	}
	if err := validateDeviceSettings(m); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	held, err := a.coord.applyMenuSettings(m, func(m map[string]any) error {
		payload, _ := json.Marshal(m)
		_, err := a.clock.fetch(r.Context(), withBody((*awtrix.Client).RawPatchSettings, payload))
		return err
	})
	if err != nil {
		writeClockError(w, err)
		return
	}
	if len(held) > 0 {
		w.Header().Set(deferredKeysHeader, strings.Join(held, ","))
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleDeviceStats(w http.ResponseWriter, r *http.Request) {
	a.proxyRead(w, r, (*awtrix.Client).RawDevice)
}

// handleDeviceScreen passes through the clock's live framebuffer
// (GET /api/v1/display/screen) so the menu app can mirror the display.
// awtrix-ng wraps the pixels: {"width":32,"height":8,"pixels":[256 ints]}
// (AWTRIX3 returned the bare 256-int array) — consumers must unwrap.
func (a *App) handleDeviceScreen(w http.ResponseWriter, r *http.Request) {
	a.proxyRead(w, r, (*awtrix.Client).RawScreen)
}

func (a *App) handleDeviceReboot(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawReboot)
}

// handleDeviceDismiss clears the currently-shown notification
// (DELETE /api/v1/notifications/active — no body).
func (a *App) handleDeviceDismiss(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawDismissNotify)
}

// handleDeviceNextApp / handleDevicePrevApp advance the clock to the next or
// previous app in its rotation (POST /api/v1/apps/next, /api/v1/apps/previous).
func (a *App) handleDeviceNextApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawNextApp)
}

func (a *App) handleDevicePrevApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawPreviousApp)
}

// proxyRead relays a clock read: its JSON body verbatim on success, the
// mapped error otherwise.
func (a *App) proxyRead(w http.ResponseWriter, r *http.Request, call deviceCall) {
	body, err := a.clock.fetch(r.Context(), call)
	if err != nil {
		writeClockError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// proxyAction runs a clock write or action and answers an empty 200, or the
// mapped error.
func (a *App) proxyAction(w http.ResponseWriter, r *http.Request, call deviceCall) {
	if _, err := a.clock.fetch(r.Context(), call); err != nil {
		writeClockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
