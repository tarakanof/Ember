package render

import "fmt"

// awtrix-ng wire encoding. awtrix-ng validates payloads strictly: one unknown
// key rejects the whole push with 422 and names the offending field, so every
// NG spelling this package emits is spelled once, here.
//
// Reference: https://blueforcer.github.io/awtrix-ng/reference/payload/

// ngMaxPayloadBytes is NG's HTTP body limit; a larger body is refused with 413
// and nothing is applied. Only the 32×8 full-frame bitmap tiles come anywhere
// near it (~2.4 KB worst case).
const ngMaxPayloadBytes = 8192

// msOf converts whole seconds to the milliseconds NG's durationMs/lifetimeMs
// take. AWTRIX3's duration/lifetime were seconds; forgetting the ×1000 is a
// silent 1000× shortening, not an error, so all conversions route through here.
func msOf(seconds int) int { return seconds * 1000 }

// bitmapOp builds NG's ["bitmap", x, y, w, h, data] draw command — the array
// form that replaced AWTRIX3's {"db": [x, y, w, h, pixels]} object. data stays
// an array of packed 0xRRGGBB ints, one of the two encodings NG accepts (the
// other is base64 RGB888), so the existing framePixels output feeds it directly.
func bitmapOp(x, y, w, h int, data []int) []any {
	return []any{"bitmap", x, y, w, h, data}
}

// scrollStatic is NG's "this text never moves" scroll object, replacing
// AWTRIX3's noScroll:true. For text known to fit the panel.
func scrollStatic() map[string]any {
	return map[string]any{"mode": "static"}
}

// scrollStaticWhenFits asks NG to leave text at rest when it fits the panel and
// scroll it only when it overflows — the native replacement for Ember's old
// len(text)<=5 character-count gate. whenFits is a string enum, NOT a bool: a
// bool is rejected with 422 field "scroll.whenFits" (verified on firmware
// 1.0.13). Set explicitly rather than relying on the default, because every
// scroll field inherits individually from the device's global scroll setting.
func scrollStaticWhenFits() map[string]any {
	return map[string]any{"whenFits": "static"}
}

// applyHold marks a pushed-app payload as "this app takes and keeps the screen"
// — Ember's display hold, formerly AWTRIX3's prio+force pair.
//
// NG has no per-payload priority: prio and force are not in the pushed-app
// schema and 422 the entire push (verified on firmware 1.0.13:
// {"code":"validationFailed","message":"unknown key \"prio\"","field":"prio"}).
// The only lever a payload has is its dwell, so a held frame requests a dwell as
// long as its own lifetime — it then occupies its rotation slot for the whole
// time Ember intends to own the screen.
//
// Actually pinning the app past the rotation is a device-level operation, not a
// payload one: PUT /api/v1/apps/active (which does accept a pushed app's name),
// or PATCH /api/v1/settings {"autoTransition": false}, or reducing the loop to a
// single app via PUT /api/v1/apps/order. Issue #69 wires that to the coordinator
// and verifies the resulting semantics on-device.
func applyHold(p map[string]any, lifetimeSeconds int) {
	p["durationMs"] = msOf(lifetimeSeconds)
}

// hexOf formats an RGB as NG's canonical colour string "#RRGGBB". NG also
// accepts bare "RRGGBB", "#RGB", [r,g,b] and packed ints, but every Ember colour
// field uses this one form: it is what the device echoes back on reads, and it
// is the form isHexColor/parseHex already validate on the way in.
func hexOf(c RGB) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }
