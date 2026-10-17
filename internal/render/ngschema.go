package render

import (
	"fmt"
	"regexp"
)

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
	ng12NotifyOnlyKeys = map[string]bool{
		"name": true, "hold": true, "stack": true, "wakeup": true, "sound": true,
	}
	ng12SoundName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	ngScrollInts  = map[string]bool{"speed": true, "gap": true, "holdMs": true}
	ngInts        = map[string]bool{"repeat": true, "durationMs": true, "lifetimeMs": true,
		"textBlinkMs": true, "textFadeMs": true}
	ngBools = map[string]bool{"hold": true, "stack": true, "wakeup": true, "soundLoop": true,
		"textCenter": true, "textInFront": true, "chartAutoscale": true, "paletteBlend": true}
)

func CheckNGPayload(p map[string]any, notification bool) []string {
	return checkNG(p, notification, ngNotifyOnlyKeys)
}

func CheckNG12Payload(p map[string]any, notification bool) []string {
	errs := checkNG(p, notification, ng12NotifyOnlyKeys)
	if v, ok := p["sound"]; ok && notification {
		errs = append(errs, checkNG12Sound(v)...)
	}
	return errs
}

func checkNG(p map[string]any, notification bool, notifyOnly map[string]bool) []string {
	var errs []string
	for k, v := range p {
		if !ngSharedKeys[k] && !(notification && notifyOnly[k]) {
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

func checkNG12Sound(v any) []string {
	if name, ok := v.(string); ok {
		if !ng12SoundName.MatchString(name) {
			return []string{fmt.Sprintf("sound = %q, not a stored name (%s)", name, ng12SoundName)}
		}
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("sound = %v (%T), want a name or a sound object", v, v)}
	}
	var errs []string
	sources := 0
	for k, fv := range m {
		var bad bool
		switch k {
		case "file":
			s, ok := fv.(string)
			bad = !ok || !ng12SoundName.MatchString(s)
			sources++
		case "rtttl":
			s, ok := fv.(string)
			bad = !ok || s == "" || len(s) > 512
			sources++
		case "track":
			n, ok := fv.(int)
			bad = !ok || n < 1 || n > 2999
			sources++
		case "loop":
			_, ok := fv.(bool)
			bad = !ok
		default:
			errs = append(errs, fmt.Sprintf("sound.%s is not an NG sound key", k))
			continue
		}
		if bad {
			errs = append(errs, fmt.Sprintf("sound.%s = %v (%T) is not a valid value", k, fv, fv))
		}
	}
	if sources != 1 {
		errs = append(errs, fmt.Sprintf("sound has %d source keys, want exactly one", sources))
	}
	return errs
}

func checkNGScroll(v any) []string {
	if s, ok := v.(string); ok {
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
