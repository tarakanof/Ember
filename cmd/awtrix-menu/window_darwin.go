//go:build darwin

package main

import (
	"fmt"

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
			winH = 476.0
		)
		w := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			foundation.Rect{Size: foundation.Size{Width: winW, Height: winH}},
			appkit.WindowStyleMaskTitled|appkit.WindowStyleMaskClosable,
			appkit.BackingStoreBuffered, false,
		)
		w.SetTitle("AWTRIX Settings")
		w.SetReleasedWhenClosed(false)
		content := w.ContentView()

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
			Origin: foundation.Point{X: 16, Y: 266},
			Size:   foundation.Size{Width: winW - 32, Height: 190},
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
		// When the well changes, write its #RRGGBB back into the text field.
		action.Set(colorWell, func(sender objc.Object) {
			colorField.SetStringValue(colorToHex(colorWell.Color()))
		})
		connView.AddSubview(newFieldLabel("Source color", foundation.Point{X: 8, Y: 14}))
		connView.AddSubview(colorField)
		connView.AddSubview(colorWell)
		connView.AddSubview(colorErr)

		content.AddSubview(connBox)

		// --- Group 2: What shows on the clock ------------------------------
		clockBox := appkit.NewBox()
		clockBox.SetTitle("What shows on the clock")
		clockBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 16, Y: 60},
			Size:   foundation.Size{Width: winW - 32, Height: 194},
		})
		clockView := appkit.NewView()
		clockBox.SetContentView(clockView)

		// Context-window glass checkbox.
		ctxCheck := appkit.NewButton()
		ctxCheck.SetButtonType(appkit.ButtonTypeSwitch)
		ctxCheck.SetTitle("Context-window glass (% used)")
		ctxCheck.SetState(boolState(form.ContextPct))
		ctxCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 110}, Size: foundation.Size{Width: 320, Height: rowH}})
		clockView.AddSubview(ctxCheck)

		// Context-window-tokens field.
		ctxField := appkit.NewTextField()
		ctxField.SetStringValue(form.ContextWindow)
		ctxField.SetPlaceholderString("blank = model default")
		ctxField.SetFrame(foundation.Rect{Origin: foundation.Point{X: 168, Y: 74}, Size: foundation.Size{Width: 100, Height: rowH}})
		ctxErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 272, Y: 78}, Size: foundation.Size{Width: 140, Height: 14}})
		clockView.AddSubview(newFieldLabel("Context-window tokens", foundation.Point{X: 8, Y: 76}))
		clockView.AddSubview(ctxField)
		clockView.AddSubview(ctxErr)

		// 5h rate-limit checkbox.
		rateCheck := appkit.NewButton()
		rateCheck.SetButtonType(appkit.ButtonTypeSwitch)
		rateCheck.SetTitle("5h rate-limit glass (% used)")
		rateCheck.SetState(boolState(form.RatePct))
		rateCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 40}, Size: foundation.Size{Width: 320, Height: rowH}})
		clockView.AddSubview(rateCheck)

		// Tool / approval detail checkbox.
		activityCheck := appkit.NewButton()
		activityCheck.SetButtonType(appkit.ButtonTypeSwitch)
		activityCheck.SetTitle("Tool / approval detail")
		activityCheck.SetState(boolState(form.ActivityDetail))
		activityCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 10}, Size: foundation.Size{Width: 320, Height: rowH}})
		clockView.AddSubview(activityCheck)

		// Recent-activity trail checkbox.
		trailCheck := appkit.NewButton()
		trailCheck.SetButtonType(appkit.ButtonTypeSwitch)
		trailCheck.SetTitle("Recent-activity trail")
		trailCheck.SetState(boolState(form.ActivityTrail))
		trailCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 144}, Size: foundation.Size{Width: 320, Height: rowH}})
		clockView.AddSubview(trailCheck)

		content.AddSubview(clockBox)

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
			keyCtxWindow: ctxErr,
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
				Source:        sourceField.StringValue(),
				ServerURL:     urlField.StringValue(),
				SourceColor:   colorField.StringValue(),
				ContextWindow: ctxField.StringValue(),
				ContextPct:     ctxCheck.State() == appkit.ControlStateValueOn,
				RatePct:        rateCheck.State() == appkit.ControlStateValueOn,
				ActivityDetail: activityCheck.State() == appkit.ControlStateValueOn,
				ActivityTrail:  trailCheck.State() == appkit.ControlStateValueOn,
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
			appkit.Application_SharedApplication().SetActivationPolicy(appkit.ApplicationActivationPolicyAccessory)
			settingsWindow = appkit.Window{}
		})
		w.SetDelegate(wd)

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
