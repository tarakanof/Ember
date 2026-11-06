package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/tarakanof/ember/internal/awtrix"
)

type settingKind int

const (
	kBool settingKind = iota
	kInt
	kColor
	kEnum
	kString
	kObject
	kNumber
	kStringOrNull
	kColorOrNull
)

type settingRule struct {
	kind     settingKind
	min, max int
	enum     map[string]bool
	maxLen   int
	obj      map[string]settingRule
}

func enumOf(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

var scrollRules = map[string]settingRule{
	"mode":      {kind: kString, maxLen: 32},
	"direction": {kind: kString, maxLen: 32},
	"entry":     {kind: kString, maxLen: 32},
	"whenFits":  {kind: kString, maxLen: 32},
	"speed":     {kind: kInt, min: 0, max: 1000},
	"gap":       {kind: kInt, min: 0, max: 64},
	"holdMs":    {kind: kInt, min: 0, max: 60000},
}

var weekdayBarRules = map[string]settingRule{
	"show":          {kind: kBool},
	"startOnMonday": {kind: kBool},
	"activeColor":   {kind: kColor},
	"inactiveColor": {kind: kColor},
}

var deviceSettingRules = map[string]settingRule{
	"brightness":           {kind: kInt, min: 0, max: 255},
	"autoBrightness":       {kind: kBool},
	"appDurationMs":        {kind: kInt, min: 1000, max: 3600000},
	"autoTransition":       {kind: kBool},
	"transitionDurationMs": {kind: kInt, min: 0, max: 60000},
	"transitionEffect":     {kind: kString, maxLen: 32},
	"textColor":            {kind: kColor},
	"uppercase":            {kind: kBool},
	"blockNavigation":      {kind: kBool},
	"timeMode":             {kind: kInt, min: 0, max: 6},
	"time24h":              {kind: kBool},
	"timeLeadingZero":      {kind: kBool},
	"timeShowSeconds":      {kind: kBool},
	"timeShowAmPm":         {kind: kBool},
	"timeSeparatorMode":    {kind: kEnum, enum: enumOf("steady", "blink", "pulse")},
	"dateOrder":            {kind: kEnum, enum: enumOf("dayMonthYear", "monthDayYear", "yearMonthDay")},
	"dateSeparator":        {kind: kEnum, enum: enumOf("dot", "slash", "dash")},
	"dateYearMode":         {kind: kEnum, enum: enumOf("none", "twoDigit", "fourDigit")},
	"dateShowWeekday":      {kind: kBool},
	"dateMonthNames":       {kind: kBool},
	"calendarHeaderColor":  {kind: kColor},
	"calendarBodyColor":    {kind: kColor},
	"calendarTextColor":    {kind: kColor},
	"timeColor":            {kind: kColorOrNull},
	"dateColor":            {kind: kColorOrNull},
	"temperatureColor":     {kind: kColorOrNull},
	"humidityColor":        {kind: kColorOrNull},
	"batteryColor":         {kind: kColorOrNull},
	"useCelsius":           {kind: kBool},
	"scroll":               {kind: kObject, obj: scrollRules},
	"weekdayBar":           {kind: kObject, obj: weekdayBarRules},
}

var legacySoundRules = map[string]settingRule{
	"soundEnabled": {kind: kBool},
	"buzzerVolume": {kind: kInt, min: 0, max: 100},
}

var mixerSoundRules = map[string]settingRule{
	"volume":      {kind: kInt, min: 0, max: 100},
	"appVolume":   {kind: kInt, min: 0, max: 100},
	"alertVolume": {kind: kInt, min: 0, max: 100},
}

type soundAPI int

const (
	soundAPIUnknown soundAPI = iota
	soundAPILegacy
	soundAPIMixer
)

func soundAPIOf(settings map[string]any) soundAPI {
	_, app := settings["appVolume"]
	_, alert := settings["alertVolume"]
	_, enabled := settings["soundEnabled"]
	_, buzzer := settings["buzzerVolume"]
	switch {
	case app || alert:
		return soundAPIMixer
	case enabled || buzzer:
		return soundAPILegacy
	}
	return soundAPIUnknown
}

type settingsSoundCache struct {
	mu   sync.Mutex
	base string
	api  soundAPI
}

func (c *settingsSoundCache) observe(base string, api soundAPI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.api = base, api
}

func (c *settingsSoundCache) forget() { c.observe("", soundAPIUnknown) }

func (c *settingsSoundCache) get(base string) soundAPI {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !sameDeviceURL(c.base, base) {
		return soundAPIUnknown
	}
	return c.api
}

func (a *App) deviceSoundAPI() soundAPI {
	if api := a.settingsSound.get(a.cfg.Load().effectiveClockURL()); api != soundAPIUnknown {
		return api
	}
	if caps, ok := a.capabilities(); ok {
		if caps.Audio.SoundObjectAPI() {
			return soundAPIMixer
		}
		return soundAPILegacy
	}
	return soundAPIUnknown
}

func isDeviceSettingKey(k string) bool {
	_, base := deviceSettingRules[k]
	_, legacy := legacySoundRules[k]
	_, mixer := mixerSoundRules[k]
	return base || legacy || mixer
}

var hexColor = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)
var printableASCII = regexp.MustCompile(`^[\x20-\x7E]*$`)

func validateDeviceSettings(m map[string]any, api soundAPI) error {
	rules := make(map[string]settingRule, len(deviceSettingRules)+len(legacySoundRules)+len(mixerSoundRules))
	maps.Copy(rules, deviceSettingRules)
	switch api {
	case soundAPIMixer:
		if k, ok := firstKeyIn(m, legacySoundRules); ok {
			return fmt.Errorf("setting %q was removed in awtrix-ng 1.2; use volume, appVolume or alertVolume", k)
		}
		maps.Copy(rules, mixerSoundRules)
	case soundAPILegacy:
		if k, ok := firstKeyIn(m, mixerSoundRules); ok {
			return fmt.Errorf("setting %q needs awtrix-ng 1.2 or later; this clock takes soundEnabled and buzzerVolume", k)
		}
		maps.Copy(rules, legacySoundRules)
	default:
		maps.Copy(rules, legacySoundRules)
		maps.Copy(rules, mixerSoundRules)
	}
	return validateAgainstRules(m, rules)
}

func firstKeyIn(m map[string]any, rules map[string]settingRule) (string, bool) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if _, ok := rules[k]; ok {
			return k, true
		}
	}
	return "", false
}

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

const deferredKeysHeader = "X-Ember-Deferred-Keys"

func (a *App) handleDeviceSettingsGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.clock.readContext(r.Context())
	defer cancel()
	before, hadBefore, err := a.coord.takeoverPriorViewContext(ctx)
	if err != nil {
		a.clock.readBudgetError(ctx, w, err)
		return
	}
	body, err := a.clock.fetch(ctx, (*awtrix.Client).RawSettings)
	if err != nil {
		a.clock.readBudgetError(ctx, w, err)
		return
	}
	var all map[string]any
	if err := json.Unmarshal(body, &all); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.settingsSound.observe(a.cfg.Load().effectiveClockURL(), soundAPIOf(all))
	out := map[string]any{}
	for k, v := range all {
		if isDeviceSettingKey(k) {
			out[k] = v
		}
	}
	p, ok, err := a.coord.takeoverPriorViewContext(ctx)
	if err != nil {
		a.clock.readBudgetError(ctx, w, err)
		return
	}
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
	if err := validateDeviceSettings(m, a.deviceSoundAPI()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := a.clock.writeContext(r.Context())
	defer cancel()
	landed := false
	held, err := a.coord.applyMenuSettings(ctx, m, func(m map[string]any) error {
		payload, _ := json.Marshal(m)
		if err := a.clock.sendWrite(ctx, withBody((*awtrix.Client).RawPatchSettings, payload)); err != nil {
			return err
		}
		landed = true
		return nil
	})
	if err != nil {
		a.clock.writeBudgetError(ctx, w, err, landed)
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

func (a *App) handleDeviceScreen(w http.ResponseWriter, r *http.Request) {
	a.proxyRead(w, r, (*awtrix.Client).RawScreen)
}

func (a *App) handleDeviceReboot(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawReboot)
}

func (a *App) handleDeviceDismiss(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawDismissNotify)
}

func (a *App) handleDeviceNextApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawNextApp)
}

func (a *App) handleDevicePrevApp(w http.ResponseWriter, r *http.Request) {
	a.proxyAction(w, r, (*awtrix.Client).RawPreviousApp)
}

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

func (a *App) proxyAction(w http.ResponseWriter, r *http.Request, call deviceCall) {
	if _, err := a.clock.fetch(r.Context(), call); err != nil {
		writeClockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
