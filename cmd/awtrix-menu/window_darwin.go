//go:build darwin

package main

import (
	"fmt"
	"time"

	"github.com/dt/awtrix-ai-status/internal/render"

	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/helper/action"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// settingsWindow is the single reused window instance. Built, read, and reset
// only on the serial main queue, so the guard check and arm are race-free.
var settingsWindow appkit.Window

// openSettingsWindow opens (or refocuses) the settings window. Safe to call
// from any goroutine — it marshals all AppKit work onto the main thread.
func openSettingsWindow(envPath string) {
	dispatch.MainQueue().DispatchAsync(func() {
		app := appkit.Application_SharedApplication()
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
		// NSApplication.activateIgnoringOtherApps is NOT bound in DarwinKit;
		// use the running-application route to bring this bundle-less binary
		// forward so its window can become key.
		appkit.RunningApplication_CurrentApplication().ActivateWithOptions(appkit.ApplicationActivateAllWindows)

		// Single reused instance: refocus the existing window if alive.
		if !settingsWindow.IsNil() {
			settingsWindow.MakeKeyAndOrderFront(nil)
			return
		}

		rec, err := readEnv(envPath)
		if err != nil {
			rec = &envRec{}
		}
		form, tokenSet := formFromEnv(rec)

		const (
			winW = 460.0
			winH = 700.0
		)
		w := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			foundation.Rect{Size: foundation.Size{Width: winW, Height: winH}},
			appkit.WindowStyleMaskTitled|appkit.WindowStyleMaskClosable,
			appkit.BackingStoreBuffered, false,
		)
		w.SetTitle("AWTRIX Settings")
		w.SetReleasedWhenClosed(false)
		content := w.ContentView()

		// --- Preview panel (top) -------------------------------------------
		// A dark backing box holding the pre-scaled 32x8 matrix preview, a
		// caption for the current number-slot card, and a live/sample badge.
		const (
			previewW = 32 * previewScale // 224
			previewH = 8 * previewScale  // 56
		)
		previewBox := appkit.NewBox()
		previewBox.SetTitle("Preview")
		previewBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 16, Y: 572},
			Size:   foundation.Size{Width: winW - 32, Height: 110},
		})
		previewView := appkit.NewView()
		previewBox.SetContentView(previewView)

		// Dark backing so the matrix pixels read against the panel.
		previewBacking := appkit.NewView()
		previewBacking.SetFrame(foundation.Rect{Origin: foundation.Point{X: 12, Y: 24}, Size: foundation.Size{Width: previewW, Height: previewH}})
		previewBacking.SetWantsLayer(true)
		if layer := previewBacking.Layer(); !layer.IsNil() {
			layer.SetBackgroundColor(appkit.Color_BlackColor().CGColor())
		}
		previewView.AddSubview(previewBacking)

		imageView := appkit.NewImageViewWithFrame(foundation.Rect{Origin: foundation.Point{X: 0, Y: 0}, Size: foundation.Size{Width: previewW, Height: previewH}})
		imageView.SetImageScaling(appkit.ImageScaleNone)
		previewBacking.AddSubview(imageView)

		previewCaption := appkit.TextField_LabelWithString("")
		previewCaption.SetFrame(foundation.Rect{Origin: foundation.Point{X: 12, Y: 4}, Size: foundation.Size{Width: previewW + 60, Height: 16}})
		previewCaption.SetFont(appkit.Font_SystemFontOfSize(10))
		previewCaption.SetTextColor(appkit.Color_SecondaryLabelColor())
		previewView.AddSubview(previewCaption)

		previewBadge := appkit.TextField_LabelWithString("")
		previewBadge.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 32 - 80, Y: 60}, Size: foundation.Size{Width: 64, Height: 16}})
		previewBadge.SetFont(appkit.Font_SystemFontOfSize(10))
		previewBadge.SetAlignment(appkit.TextAlignmentRight)
		previewView.AddSubview(previewBadge)

		pw := &previewWidget{imageView: imageView, caption: previewCaption, badge: previewBadge}

		content.AddSubview(previewBox)

		// --- Group 1: Connection -------------------------------------------
		// Box content coordinates are relative to the box's content view.
		const (
			rowH   = 24.0
			labelW = 96.0
			fieldX = 108.0
			fieldW = 300.0
			errX   = fieldX
			errW   = fieldW
		)

		connBox := appkit.NewBox()
		connBox.SetTitle("Connection")
		connBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 16, Y: 348},
			Size:   foundation.Size{Width: winW - 32, Height: 212},
		})
		connView := appkit.NewView()
		connBox.SetContentView(connView)

		// Source
		sourceField := appkit.NewTextField()
		sourceField.SetStringValue(form.Source)
		sourceField.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: 132}, Size: foundation.Size{Width: fieldW, Height: rowH}})
		sourceErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: errX, Y: 116}, Size: foundation.Size{Width: errW, Height: 14}})
		connView.AddSubview(newFieldLabel("Source", foundation.Point{X: 8, Y: 134}))
		connView.AddSubview(sourceField)
		connView.AddSubview(sourceErr)

		// Server URL
		urlField := appkit.NewTextField()
		urlField.SetStringValue(form.ServerURL)
		urlField.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: 92}, Size: foundation.Size{Width: fieldW, Height: rowH}})
		urlErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: errX, Y: 76}, Size: foundation.Size{Width: errW, Height: 14}})
		connView.AddSubview(newFieldLabel("Server URL", foundation.Point{X: 8, Y: 94}))
		connView.AddSubview(urlField)
		connView.AddSubview(urlErr)

		// Token (secure, write-only)
		tokenField := appkit.NewSecureTextField()
		if tokenSet {
			tokenField.SetPlaceholderString("(set)")
		} else {
			tokenField.SetPlaceholderString("(unset)")
		}
		tokenField.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: 52}, Size: foundation.Size{Width: fieldW, Height: rowH}})
		tokenErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: errX, Y: 36}, Size: foundation.Size{Width: errW, Height: 14}})
		connView.AddSubview(newFieldLabel("Token", foundation.Point{X: 8, Y: 54}))
		connView.AddSubview(tokenField)
		connView.AddSubview(tokenErr)

		// Source color: text field is the source of truth; color well mirrors.
		colorField := appkit.NewTextField()
		colorField.SetStringValue(form.SourceColor)
		colorField.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: 12}, Size: foundation.Size{Width: 120, Height: rowH}})
		colorWell := appkit.NewColorWell()
		colorWell.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX + 132, Y: 10}, Size: foundation.Size{Width: 44, Height: rowH + 4}})
		if c, ok := parseHexColor(form.SourceColor); ok {
			colorWell.SetColor(c)
		}
		colorErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: errX + 188, Y: 16}, Size: foundation.Size{Width: 120, Height: 14}})
		// Color-well action wired below (after the preview widget exists) so it
		// can also trigger a live re-render.
		connView.AddSubview(newFieldLabel("Source color", foundation.Point{X: 8, Y: 14}))
		connView.AddSubview(colorField)
		connView.AddSubview(colorWell)
		connView.AddSubview(colorErr)

		// One-line note explaining what the source color affects in the preview.
		colorNote := appkit.TextField_LabelWithString("Source color tints the robot mark & X/Y digits in the preview.")
		colorNote.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 168}, Size: foundation.Size{Width: fieldW + 100, Height: 16}})
		colorNote.SetFont(appkit.Font_SystemFontOfSize(10))
		colorNote.SetTextColor(appkit.Color_SecondaryLabelColor())
		connView.AddSubview(colorNote)

		// When the well changes, write its #RRGGBB back into the text field and
		// re-tint the preview live.
		action.Set(colorWell, func(sender objc.Object) {
			colorField.SetStringValue(colorToHex(colorWell.Color()))
			pw.onFormChanged()
		})

		content.AddSubview(connBox)

		// --- Group 2: Display elements -------------------------------------
		// Grouped by the screen region each element occupies. Checkbox rows on
		// the left; a small thumbnail of the element on the right.
		clockBox := appkit.NewBox()
		clockBox.SetTitle("Display elements")
		clockBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 16, Y: 54},
			Size:   foundation.Size{Width: winW - 32, Height: 284},
		})
		clockView := appkit.NewView()
		clockBox.SetContentView(clockView)

		const (
			checkW = 250.0
			thumbX = 332.0
			thumbW = 64.0
			thumbH = 16.0
		)
		// newCheck builds a switch-style checkbox at (x, y).
		newCheck := func(title string, on bool, x, y float64) appkit.Button {
			b := appkit.NewButton()
			b.SetButtonType(appkit.ButtonTypeSwitch)
			b.SetTitle(title)
			b.SetState(boolState(on))
			b.SetFrame(foundation.Rect{Origin: foundation.Point{X: x, Y: y}, Size: foundation.Size{Width: checkW, Height: rowH}})
			clockView.AddSubview(b)
			return b
		}
		// newSubHeader builds a plain bold-ish sub-section label at (x, y).
		newSubHeader := func(title string, y float64) {
			lbl := appkit.TextField_LabelWithString(title)
			lbl.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: y}, Size: foundation.Size{Width: 320, Height: 16}})
			lbl.SetFont(appkit.Font_BoldSystemFontOfSize(11))
			lbl.SetTextColor(appkit.Color_SecondaryLabelColor())
			clockView.AddSubview(lbl)
		}
		// addThumb places a 2x element thumbnail to the right of a row at y.
		addThumb := func(y float64, mut func(s *render.Session)) {
			iv := appkit.NewImageViewWithFrame(foundation.Rect{Origin: foundation.Point{X: thumbX, Y: y + 3}, Size: foundation.Size{Width: thumbW, Height: thumbH}})
			iv.SetImageScaling(appkit.ImageScaleNone)
			iv.SetImage(thumbImage(mut))
			clockView.AddSubview(iv)
		}

		// -- Number slot (rotates) -----------------------------------------
		newSubHeader("Number slot (rotates):", 234)

		ctxNumCheck := newCheck("Context %", form.ContextNumber, 16, 210)
		addThumb(210, func(s *render.Session) { s.ContextPct = ptr(47); s.ContextNumber = true })

		rateCheck := newCheck("5h rate-limit %", form.RatePct, 16, 186)
		addThumb(186, func(s *render.Session) { s.RateWindowPct = ptr(47) })

		resetCheck := newCheck("Reset countdown", form.RateReset, 16, 162)
		addThumb(162, func(s *render.Session) {
			s.RateResetAt = time.Now().Add(3 * time.Hour).Unix()
			s.RateReset = true
		})

		activityCheck := newCheck("Tool / approval detail", form.ActivityDetail, 16, 138)
		trailCheck := newCheck("↳ Recent-activity trail", form.ActivityTrail, 36, 114)

		// -- Right edge ----------------------------------------------------
		newSubHeader("Right edge:", 86)

		ctxCheck := newCheck("Context-window glass", form.ContextPct, 16, 62)
		addThumb(62, func(s *render.Session) { s.ContextPct = ptr(47) })

		// -- Bottom row ----------------------------------------------------
		newSubHeader("Bottom row:", 34)

		rateBarCheck := newCheck("5h rate as bottom bar", form.RateBottomBar, 16, 10)
		addThumb(10, func(s *render.Session) {
			s.RateWindowPct = ptr(47)
			s.RateBottomBar = true
		})

		content.AddSubview(clockBox)

		// --- Preview wiring ------------------------------------------------
		// readCurrentForm mirrors the Save handler's control reads (minus the
		// write-only token, which the preview does not use) so the preview
		// reflects the live, uncommitted control state.
		readCurrentForm := func() settingsForm {
			return settingsForm{
				Source:         sourceField.StringValue(),
				ServerURL:      urlField.StringValue(),
				SourceColor:    colorField.StringValue(),
				ContextPct:     ctxCheck.State() == appkit.ControlStateValueOn,
				RatePct:        rateCheck.State() == appkit.ControlStateValueOn,
				ActivityDetail: activityCheck.State() == appkit.ControlStateValueOn,
				ActivityTrail:  trailCheck.State() == appkit.ControlStateValueOn,
				ContextNumber:  ctxNumCheck.State() == appkit.ControlStateValueOn,
				RateBottomBar:  rateBarCheck.State() == appkit.ControlStateValueOn,
				RateReset:      resetCheck.State() == appkit.ControlStateValueOn,
			}
		}
		pw.formProvider = readCurrentForm

		// Re-render the preview whenever any element toggles.
		for _, c := range []appkit.Button{ctxCheck, rateCheck, resetCheck, activityCheck, trailCheck, ctxNumCheck, rateBarCheck} {
			c := c
			action.Set(c, func(sender objc.Object) { pw.onFormChanged() })
		}

		// --- Footer: Cancel / Save ----------------------------------------
		// A general error line (e.g. write failure) above the buttons.
		generalErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 16, Y: 34}, Size: foundation.Size{Width: winW - 32, Height: 14}})
		content.AddSubview(generalErr)

		cancelBtn := appkit.NewButtonWithTitle("Cancel")
		cancelBtn.SetBezelStyle(appkit.BezelStyleRounded)
		cancelBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 200, Y: 8}, Size: foundation.Size{Width: 88, Height: 28}})
		action.Set(cancelBtn, func(sender objc.Object) {
			w.PerformClose(nil)
		})
		content.AddSubview(cancelBtn)

		saveBtn := appkit.NewButtonWithTitle("Save")
		saveBtn.SetBezelStyle(appkit.BezelStyleRounded)
		saveBtn.SetKeyEquivalent("\r") // default button: Return triggers Save.
		saveBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 104, Y: 8}, Size: foundation.Size{Width: 88, Height: 28}})

		// Map producer.env keys to their inline error labels.
		errLabels := map[string]appkit.TextField{
			keySource:    sourceErr,
			keyServerURL: urlErr,
			keyToken:     tokenErr,
			keyColor:     colorErr,
		}
		clearErrors := func() {
			for _, lbl := range errLabels {
				lbl.SetStringValue("")
			}
			generalErr.SetStringValue("")
		}

		action.Set(saveBtn, func(sender objc.Object) {
			w.EndEditingFor(nil) // commit any in-progress field edit so StringValue is current
			clearErrors()
			f := settingsForm{
				Source:         sourceField.StringValue(),
				ServerURL:      urlField.StringValue(),
				SourceColor:    colorField.StringValue(),
				ContextPct:     ctxCheck.State() == appkit.ControlStateValueOn,
				RatePct:        rateCheck.State() == appkit.ControlStateValueOn,
				ActivityDetail: activityCheck.State() == appkit.ControlStateValueOn,
				ActivityTrail:  trailCheck.State() == appkit.ControlStateValueOn,
				ContextNumber:  ctxNumCheck.State() == appkit.ControlStateValueOn,
				RateBottomBar:  rateBarCheck.State() == appkit.ControlStateValueOn,
				RateReset:      resetCheck.State() == appkit.ControlStateValueOn,
				Token:          tokenField.StringValue(),
			}
			if errs := validateForm(f); len(errs) > 0 {
				for key, msg := range errs {
					if lbl, ok := errLabels[key]; ok {
						lbl.SetStringValue(msg)
					} else {
						generalErr.SetStringValue(msg)
					}
				}
				return
			}
			// Re-read fresh so unknown keys are preserved (last-writer-wins).
			rec2, err := readEnv(envPath)
			if err != nil {
				rec2 = &envRec{}
			}
			applyForm(rec2, f)
			if err := writeEnvAtomic(envPath, rec2.serialize()); err != nil {
				generalErr.SetStringValue(fmt.Sprintf("could not save: %v", err))
				return
			}
			w.PerformClose(nil)
		})
		content.AddSubview(saveBtn)

		// --- Window delegate: revert to Accessory + rearm guard on close. --
		wd := &appkit.WindowDelegate{}
		wd.SetWindowWillClose(func(notification foundation.Notification) {
			pw.stop()
			appkit.Application_SharedApplication().SetActivationPolicy(appkit.ApplicationActivationPolicyAccessory)
			settingsWindow = appkit.Window{}
		})
		w.SetDelegate(wd)

		// Seed the preview from /state (sample fallback) and start the
		// number-slot rotation before the window becomes visible.
		pw.base, pw.live = fetchBaseSession(urlField.StringValue(), 2*time.Second)
		pw.render()
		pw.startRotation()

		// Arm the single-instance guard before showing.
		settingsWindow = w
		w.Center()
		w.MakeKeyAndOrderFront(nil)
	})
}

// newFieldLabel builds a right-of-nothing left-aligned static label at origin.
func newFieldLabel(title string, origin foundation.Point) appkit.TextField {
	lbl := appkit.TextField_LabelWithString(title)
	lbl.SetFrame(foundation.Rect{Origin: origin, Size: foundation.Size{Width: 96, Height: 18}})
	return lbl
}

// newErrorLabel builds an empty red label used for inline validation messages.
func newErrorLabel(frame foundation.Rect) appkit.TextField {
	lbl := appkit.TextField_LabelWithString("")
	lbl.SetTextColor(appkit.Color_SystemRedColor())
	lbl.SetFrame(frame)
	return lbl
}

// boolState maps a Go bool to an NSControl on/off state.
func boolState(b bool) appkit.ControlStateValue {
	if b {
		return appkit.ControlStateValueOn
	}
	return appkit.ControlStateValueOff
}

// parseHexColor converts a #RRGGBB string into an sRGB NSColor. ok is false for
// any value that is not exactly #RRGGBB (so the color well stays at default).
func parseHexColor(s string) (appkit.Color, bool) {
	if !hexColorRe.MatchString(s) {
		return appkit.Color{}, false
	}
	var r, g, b int
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return appkit.Color{}, false
	}
	c := appkit.Color_ColorWithSRGBRedGreenBlueAlpha(
		float64(r)/255.0, float64(g)/255.0, float64(b)/255.0, 1.0)
	return c, true
}

// colorToHex converts an NSColor (via the sRGB color space) to #RRGGBB.
func colorToHex(c appkit.Color) string {
	srgb := c.ColorUsingColorSpace(appkit.ColorSpace_SRGBColorSpace())
	if srgb.IsNil() {
		srgb = c
	}
	to8 := func(f float64) int {
		v := int(f*255.0 + 0.5)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return v
	}
	return fmt.Sprintf("#%02x%02x%02x", to8(srgb.RedComponent()), to8(srgb.GreenComponent()), to8(srgb.BlueComponent()))
}

// thumbImage renders a 2x-scaled preview of a single element for the option row.
func thumbImage(mut func(s *render.Session)) appkit.Image {
	base := sampleBaseSession()
	// Strip everything optional so only what `mut` enables shows.
	base.ContextPct = nil
	base.RateWindowPct = nil
	base.RateResetAt = 0
	base.RateReset = false
	base.ContextNumber = false
	base.RateBottomBar = false
	base.Activity = ""
	mut(&base)
	cards := render.AvailableCards(base)
	card := cards[len(cards)-1] // the element we just enabled (XY is cards[0])
	robot := render.RGB{R: 0xaa, G: 0x66, B: 0xff}
	frame := render.ComposeFrame(base, 1, 1, card, robot, []render.Session{base}, time.Now())
	pix, w, h := render.RenderRGBA(frame, 2)
	return rgbaToImage(pix, w, h)
}
