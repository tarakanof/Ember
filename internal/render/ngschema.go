package render

import "fmt"

// awtrix-ng 1.1.2's payload schema (reference/payload): 35 top-level keys
// valid on pushed apps and notifications, plus 7 read only on notifications,
// "exactly 42". One unknown key, scroll field or enum word rejects the whole
// push with 422, so the tests of every package that builds a payload check it
// against this table with CheckNGPayload.
var (
	ngSharedKeys = map[string]bool{
		"text": true, "textCase": true, "font": true, "textColor": true,
		"textBlinkMs": true, "textFadeMs": true, "textCenter": true, "scroll": true,
		"textOffsetX": true, "textInFront": true,
		"icon": true, "iconMode": true, "iconOffsetX": true, "iconGap": true, "icons": true,
		"durationMs": true, "lifetimeMs": true, "lifetimeExpiry": true, "repeat": true,
		"backgroundColor": true, "overlay": true, "draw": true,
		"barChart": true, "lineChart": true, "chartAutoscale": true, "chartColor": true,
		"progress": true, "progressColor": true, "progressTrackColor": true,
		"effect": true, "effectSpeed": true,
		"palette": true, "paletteBlend": true, "paletteSpan": true, "paletteSpeed": true,
	}
	ngNotifyOnlyKeys = map[string]bool{
		"name": true, "hold": true, "stack": true, "wakeup": true,
		"sound": true, "soundRtttl": true, "soundLoop": true,
	}
	// ngEnums lists the words NG accepts for each string-enum key, including
	// the scroll object's fields as "scroll.<field>".
	ngEnums = map[string]map[string]bool{
		"textCase":         {"inherit": true, "upper": true, "asTyped": true},
		"font":             {"small": true, "large": true},
		"iconMode":         {"fixed": true, "pushOnce": true, "push": true},
		"lifetimeExpiry":   {"remove": true, "mark": true},
		"overlay":          {"rain": true, "snow": true, "drizzle": true, "storm": true, "thunder": true, "frost": true},
		"scroll.mode":      {"static": true, "wrap": true, "loop": true, "bounce": true},
		"scroll.direction": {"left": true, "right": true},
		"scroll.entry":     {"inline": true, "offscreen": true},
		"scroll.whenFits":  {"static": true, "scroll": true},
	}
	// ngScrollInts are the scroll object's non-negative integer fields.
	ngScrollInts = map[string]bool{"speed": true, "gap": true, "holdMs": true}
	// ngInts are top-level keys NG reads as non-negative integers.
	ngInts = map[string]bool{"repeat": true, "durationMs": true, "lifetimeMs": true,
		"textBlinkMs": true, "textFadeMs": true}
	// ngBools are top-level keys NG reads as booleans.
	ngBools = map[string]bool{"hold": true, "stack": true, "wakeup": true, "soundLoop": true,
		"textCenter": true, "textInFront": true, "chartAutoscale": true, "paletteBlend": true}
)

// CheckNGPayload returns every reason NG 1.1.2 would reject p with 422: a key
// outside the schema (or a notification-only key on a pushed app), an enum
// word outside its list, an unknown scroll field, or a wrongly typed
// integer/boolean. nil means p passes. notification selects the
// POST /api/v1/notifications key set. Used by tests.
func CheckNGPayload(p map[string]any, notification bool) []string {
	var errs []string
	for k, v := range p {
		if !ngSharedKeys[k] && !(notification && ngNotifyOnlyKeys[k]) {
			errs = append(errs, fmt.Sprintf("key %q not in NG's schema (notification=%v)", k, notification))
			continue
		}
		if k == "scroll" {
			errs = append(errs, checkNGScroll(v)...)
			continue
		}
		if enum, ok := ngEnums[k]; ok {
			if s, _ := v.(string); !enum[s] {
				errs = append(errs, fmt.Sprintf("%s = %v, not one of NG's values", k, v))
			}
		}
		if ngInts[k] && !isNonNegInt(v) {
			errs = append(errs, fmt.Sprintf("%s = %v (%T), want a non-negative int", k, v, v))
		}
		if _, ok := v.(bool); ngBools[k] && !ok {
			errs = append(errs, fmt.Sprintf("%s = %v (%T), want a bool", k, v, v))
		}
	}
	return errs
}

func checkNGScroll(v any) []string {
	if s, ok := v.(string); ok { // shorthand for the mode alone
		if !ngEnums["scroll.mode"][s] {
			return []string{fmt.Sprintf("scroll = %q, not an NG scroll mode", s)}
		}
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("scroll = %v (%T), want an object or mode string", v, v)}
	}
	var errs []string
	for f, fv := range m {
		switch {
		case ngScrollInts[f]:
			if !isNonNegInt(fv) {
				errs = append(errs, fmt.Sprintf("scroll.%s = %v, want a non-negative int", f, fv))
			}
		case ngEnums["scroll."+f] != nil:
			if s, _ := fv.(string); !ngEnums["scroll."+f][s] {
				errs = append(errs, fmt.Sprintf("scroll.%s = %v, not one of NG's values", f, fv))
			}
		default:
			errs = append(errs, fmt.Sprintf("scroll.%s is not an NG scroll field", f))
		}
	}
	return errs
}

func isNonNegInt(v any) bool {
	n, ok := v.(int)
	return ok && n >= 0
}
