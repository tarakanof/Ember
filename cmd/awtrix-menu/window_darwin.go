//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
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

// settingsDelegate retains the window's delegate for the lifetime of the window.
// NSWindow.delegate is a WEAK reference, so without a Go-side reference the
// DarwinKit delegate proxy is garbage-collected after openSettingsWindow
// returns; AppKit then messages a freed delegate on close (windowShouldClose:/
// windowWillClose:) → SIGSEGV. Holding it here keeps it alive.
var settingsDelegate *appkit.WindowDelegate

// settingsTabView is the window's tab view, retained so a second open request
// can switch tabs on the already-open window. Reset alongside settingsWindow.
var settingsTabView appkit.TabView

// Tab indices within the settings window.
const (
	tabConnection = 0
	tabDisplay    = 1
	tabPomodoro   = 2
	tabApp        = 3
)

// openSettingsWindow opens (or refocuses) the settings window. Safe to call
// from any goroutine — it marshals all AppKit work onto the main thread.
func openSettingsWindow(envPath string) { openSettingsWindowOnTab(envPath, tabConnection) }

// openSettingsWindowOnTab opens (or refocuses) the settings window and selects
// the given tab. Safe to call from any goroutine.
func openSettingsWindowOnTab(envPath string, tab int) {
	dispatch.MainQueue().DispatchAsync(func() {
		app := appkit.Application_SharedApplication()
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
		// NSApplication.activateIgnoringOtherApps is NOT bound in DarwinKit;
		// use the running-application route to bring this bundle-less binary
		// forward so its window can become key.
		appkit.RunningApplication_CurrentApplication().ActivateWithOptions(appkit.ApplicationActivateAllWindows)

		// Single reused instance: refocus the existing window if alive,
		// switching to the requested tab.
		if !settingsWindow.IsNil() {
			if !settingsTabView.IsNil() {
				settingsTabView.SelectTabViewItemAtIndex(tab)
			}
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
			winH = 902.0
		)
		w := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			foundation.Rect{Size: foundation.Size{Width: winW, Height: winH}},
			appkit.WindowStyleMaskTitled|appkit.WindowStyleMaskClosable,
			appkit.BackingStoreBuffered, false,
		)
		w.SetTitle("AWTRIX Settings")
		w.SetReleasedWhenClosed(false)
		content := w.ContentView()

		// --- Tab view ------------------------------------------------------
		// Fills the area above the footer (footer stays at Y 8–48). Each tab's
		// boxes are re-parented onto its container view below; NSTabView keeps
		// all item views alive so the Save/preview closures still read them.
		const tabH = winH - 60 - 36 // tab content height (minus tab-bar+inset)
		tabView := appkit.NewTabView()
		tabView.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 8, Y: 52},
			Size:   foundation.Size{Width: winW - 16, Height: winH - 60},
		})

		connTabView := appkit.NewView()
		displayTabView := appkit.NewView()
		pomoTabView := appkit.NewView()
		appTabView := appkit.NewView()

		mkItem := func(label string, v appkit.View) {
			it := appkit.NewTabViewItem()
			it.SetLabel(label)
			it.SetView(v)
			tabView.AddTabViewItem(it)
		}
		mkItem("Connection", connTabView) // tabConnection
		mkItem("Display", displayTabView) // tabDisplay
		mkItem("Pomodoro", pomoTabView)   // tabPomodoro
		mkItem("App", appTabView)         // tabApp
		content.AddSubview(tabView)
		settingsTabView = tabView

		// --- Preview panel (top) -------------------------------------------
		// A dark backing box holding the pre-scaled 32x8 matrix preview, a
		// caption for the current number-slot card, and a live/sample badge.
		const (
			previewW    = 32 * previewScale // 384
			previewH    = 8 * previewScale  // 96
			previewBoxW = winW - 32         // 428
			// previewView (the box content view) is inset ~6px each side.
			previewContentW = previewBoxW - 12
			backingX        = (previewContentW - previewW) / 2 // center the matrix
		)
		previewBox := appkit.NewBox()
		previewBox.SetTitle("Preview")
		previewBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 8, Y: tabH - 8 - 154},
			Size:   foundation.Size{Width: previewBoxW, Height: 154},
		})
		previewView := appkit.NewView()
		previewBox.SetContentView(previewView)

		// Dark backing so the matrix pixels read against the panel.
		previewBacking := appkit.NewView()
		previewBacking.SetFrame(foundation.Rect{Origin: foundation.Point{X: backingX, Y: 28}, Size: foundation.Size{Width: previewW, Height: previewH}})
		previewBacking.SetWantsLayer(true)
		if layer := previewBacking.Layer(); !layer.IsNil() {
			layer.SetBackgroundColor(appkit.Color_BlackColor().CGColor())
		}
		previewView.AddSubview(previewBacking)

		imageView := appkit.NewImageViewWithFrame(foundation.Rect{Origin: foundation.Point{X: 0, Y: 0}, Size: foundation.Size{Width: previewW, Height: previewH}})
		imageView.SetImageScaling(appkit.ImageScaleNone)
		previewBacking.AddSubview(imageView)

		// Caption (left) and live/sample badge (right) share the row below the
		// matrix, since the full-width matrix leaves no room for a top-right badge.
		previewCaption := appkit.TextField_LabelWithString("")
		previewCaption.SetFrame(foundation.Rect{Origin: foundation.Point{X: backingX, Y: 8}, Size: foundation.Size{Width: 300, Height: 16}})
		previewCaption.SetFont(appkit.Font_SystemFontOfSize(10))
		previewCaption.SetTextColor(appkit.Color_SecondaryLabelColor())
		previewView.AddSubview(previewCaption)

		previewBadge := appkit.TextField_LabelWithString("")
		previewBadge.SetFrame(foundation.Rect{Origin: foundation.Point{X: backingX + previewW - 88, Y: 8}, Size: foundation.Size{Width: 88, Height: 16}})
		previewBadge.SetFont(appkit.Font_SystemFontOfSize(10))
		previewBadge.SetAlignment(appkit.TextAlignmentRight)
		previewView.AddSubview(previewBadge)

		pw := &previewWidget{imageView: imageView, caption: previewCaption, badge: previewBadge}

		displayTabView.AddSubview(previewBox)

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
			Origin: foundation.Point{X: 8, Y: tabH - 8 - 212},
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

		connTabView.AddSubview(connBox)

		// --- Group 2: Display elements -------------------------------------
		// Grouped by the screen region each element occupies. Checkbox rows on
		// the left; a small thumbnail of the element on the right.
		clockBox := appkit.NewBox()
		clockBox.SetTitle("Display elements")
		clockBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 8, Y: tabH - 8 - 154 - 8 - 284},
			Size:   foundation.Size{Width: winW - 32, Height: 284},
		})
		clockView := appkit.NewView()
		clockBox.SetContentView(clockView)

		const (
			checkW = 250.0
			thumbX = 332.0
			thumbW = 64.0 // 32 * thumbScale
			thumbH = 16.0 // 8 * thumbScale
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
		// addThumb places a full-display thumbnail at row y (vertically centered)
		// showing only element r lit in its true position on the dark panel.
		addThumb := func(y float64, r thumbRegion, mut func(s *render.Session)) {
			iv := appkit.NewImageViewWithFrame(foundation.Rect{Origin: foundation.Point{X: thumbX, Y: y + 4}, Size: foundation.Size{Width: thumbW, Height: thumbH}})
			iv.SetImageScaling(appkit.ImageScaleNone)
			iv.SetImage(elementThumb(mut, r))
			clockView.AddSubview(iv)
		}

		// -- Number slot (rotates) -----------------------------------------
		newSubHeader("Number slot (rotates):", 234)

		ctxNumCheck := newCheck("Context %", form.ContextNumber, 16, 210)
		addThumb(210, numberRegion, func(s *render.Session) { s.ContextPct = ptr(47); s.ContextNumber = true })

		rateCheck := newCheck("5h rate-limit %", form.RatePct, 16, 186)
		addThumb(186, numberRegion, func(s *render.Session) { s.RateWindowPct = ptr(47) })

		resetCheck := newCheck("Reset countdown", form.RateReset, 16, 162)
		addThumb(162, numberRegion, func(s *render.Session) {
			s.RateResetAt = time.Now().Add(3 * time.Hour).Unix()
			s.RateReset = true
		})

		activityCheck := newCheck("Tool / approval detail", form.ActivityDetail, 16, 138)
		trailCheck := newCheck("↳ Recent-activity trail", form.ActivityTrail, 36, 114)

		// -- Right edge ----------------------------------------------------
		newSubHeader("Right edge:", 86)

		ctxCheck := newCheck("Context-window glass", form.ContextPct, 16, 62)
		addThumb(62, glassRegion, func(s *render.Session) { s.ContextPct = ptr(47) })

		// -- Bottom row ----------------------------------------------------
		newSubHeader("Bottom row:", 34)

		rateBarCheck := newCheck("5h rate as bottom bar", form.RateBottomBar, 16, 10)
		addThumb(10, barRegion, func(s *render.Session) {
			s.RateWindowPct = ptr(47)
			s.RateBottomBar = true
		})

		displayTabView.AddSubview(clockBox)

		// groupClosed is set when the window closes so in-flight launch-at-login
		// goroutines skip touching now-freed AppKit controls in their callback.
		var groupClosed atomic.Bool

		// --- Group 3: Launch at login --------------------------------------
		home, _ := os.UserHomeDir()
		uid := os.Getuid()
		iconPrefs := loadMenuPrefs(menuPrefsPath(home))
		clearIconCacheAndRefresh := func(envPath string) { refreshTrayIcon(envPath) }
		selfDir := ""
		if exe, err := os.Executable(); err == nil {
			selfDir = filepath.Dir(exe)
		}

		launchBox := appkit.NewBox()
		launchBox.SetTitle("Launch at login")
		launchBox.SetFrame(foundation.Rect{
			Origin: foundation.Point{X: 8, Y: tabH - 8 - 150},
			Size:   foundation.Size{Width: winW - 32, Height: 150},
		})
		launchView := appkit.NewView()
		launchBox.SetContentView(launchView)

		rowTitles := map[string]string{
			"com.awtrix-ai-status.menu":      "Menu bar app",
			"com.awtrix-ai-status.heartbeat": "Claude producer",
			"com.awtrix-ai-status.codex":     "Codex producer",
		}
		rowY := map[string]float64{
			"com.awtrix-ai-status.menu":      98,
			"com.awtrix-ai-status.heartbeat": 70,
			"com.awtrix-ai-status.codex":     42,
		}
		launchErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 8, Y: 12}, Size: foundation.Size{Width: winW - 48, Height: 14}})
		launchView.AddSubview(launchErr)

		// Info line explaining the install/uninstall model, temporarily replaced
		// by the "restart claude" hint after installing the Claude producer.
		// Secondary color so it never reads as an error (errors use launchErr).
		const launchInfoNote = "On installs (launches at login, shown in System Settings → Login Items); Off uninstalls."
		launchNote := appkit.TextField_LabelWithString(launchInfoNote)
		launchNote.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 26}, Size: foundation.Size{Width: winW - 48, Height: 14}})
		launchNote.SetFont(appkit.Font_SystemFontOfSize(10))
		launchNote.SetTextColor(appkit.Color_SecondaryLabelColor())
		launchView.AddSubview(launchNote)

		for i := range managedComponents {
			c := managedComponents[i] // capture per-iteration copy
			y := rowY[c.label]
			st := detectState(c, home, uid)

			check := appkit.NewButton()
			check.SetButtonType(appkit.ButtonTypeSwitch)
			check.SetTitle(rowTitles[c.label])
			check.SetState(boolState(st.launchAtLogin()))
			check.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: y}, Size: foundation.Size{Width: 150, Height: rowH}})
			launchView.AddSubview(check)

			stateLbl := appkit.TextField_LabelWithString(st.stateLabel())
			stateLbl.SetFrame(foundation.Rect{Origin: foundation.Point{X: 166, Y: y + 2}, Size: foundation.Size{Width: 92, Height: 18}})
			stateLbl.SetFont(appkit.Font_SystemFontOfSize(11))
			stateLbl.SetTextColor(appkit.Color_SecondaryLabelColor())
			launchView.AddSubview(stateLbl)

			// The menu app row is read-only: the app cannot cleanly install or
			// uninstall itself from its own running process. Show a disabled
			// checkbox + a CLI hint.
			if c.binary == "" {
				check.SetEnabled(false)
				cliNote := appkit.TextField_LabelWithString("manage via CLI")
				cliNote.SetFrame(foundation.Rect{Origin: foundation.Point{X: 264, Y: y + 2}, Size: foundation.Size{Width: 140, Height: 18}})
				cliNote.SetFont(appkit.Font_SystemFontOfSize(10))
				cliNote.SetTextColor(appkit.Color_TertiaryLabelColor())
				launchView.AddSubview(cliNote)
				continue
			}

			// Producer row: checkbox installs (on) / uninstalls (off).
			var busy atomic.Bool

			refresh := func() {
				ns := detectState(c, home, uid)
				check.SetState(boolState(ns.launchAtLogin()))
				stateLbl.SetStringValue(ns.stateLabel())
			}

			runOps := func(successNote string, want bool) {
				if !busy.CompareAndSwap(false, true) {
					return // an operation for this row is already in flight
				}
				go func() {
					cur := detectState(c, home, uid)
					binPath := resolveBinary(c.binary, exec.LookPath, home, selfDir)
					ops := planToggle(c, cur, want, binPath)
					var err error
					if binPath == "" && len(ops) > 0 {
						err = fmt.Errorf("%s not found — build/install it first", c.binary)
					} else {
						for _, o := range ops {
							if err = runOp(o); err != nil {
								break
							}
						}
					}
					ran := len(ops) > 0
					dispatch.MainQueue().DispatchAsync(func() {
						if groupClosed.Load() {
							return // window closed mid-op; controls are gone
						}
						if err != nil {
							launchErr.SetStringValue(err.Error())
							launchNote.SetStringValue(launchInfoNote)
						} else {
							launchErr.SetStringValue("")
							if ran && successNote != "" {
								launchNote.SetStringValue(successNote)
							} else {
								launchNote.SetStringValue(launchInfoNote)
							}
						}
						refresh()
						busy.Store(false)
					})
				}()
			}

			action.Set(check, func(sender objc.Object) {
				want := check.State() == appkit.ControlStateValueOn
				// Turning OFF uninstalls (destructive) — confirm, and revert the
				// checkbox if the user backs out.
				if !want && !confirmUninstall(rowTitles[c.label]) {
					check.SetState(appkit.ControlStateValueOn)
					return
				}
				note := ""
				if want && c.binary == "awtrix-claude-producer" {
					note = "Restart claude for it to pick up the new hooks."
				}
				runOps(note, want)
			})
		}
		appTabView.AddSubview(launchBox)

		// --- App icon picker -----------------------------------------------
		// Laid out top-down below launchBox (which sits at Y = tabH-8-150, so
		// its bottom edge is tabH-158). 8px gaps, no overlap.
		const (
			launchBottom = tabH - 158
			appIconHdrY  = launchBottom - 8 - 16 // header row
			swatchY      = appIconHdrY - 8 - 56  // 56px swatch row
			trayHdrY     = swatchY - 8 - 16      // tray header row
			yClaude      = trayHdrY - 8 - 26     // glyph dropdown rows (26px)
			yCodex       = yClaude - 8 - 26
			yIdle        = yCodex - 8 - 26
		)

		appIconHdr := appkit.TextField_LabelWithString("App icon (dock icon while this window is open)")
		appIconHdr.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: appIconHdrY}, Size: foundation.Size{Width: 360, Height: 16}})
		appIconHdr.SetFont(appkit.Font_BoldSystemFontOfSize(11))
		appTabView.AddSubview(appIconHdr)

		var iconButtons []appkit.Button
		for i, pal := range appIconPalettes {
			pal := pal
			b := appkit.NewButton()
			b.SetButtonType(appkit.ButtonTypeOnOff)
			b.SetBordered(true)
			b.SetTitle("")
			if data, err := appIconPNG(pal); err == nil {
				img := appkit.NewImageWithData(data)
				b.SetImage(img)
				b.SetImageScaling(appkit.ImageScaleProportionallyDown)
			}
			b.SetFrame(foundation.Rect{Origin: foundation.Point{X: float64(8 + i*64), Y: swatchY}, Size: foundation.Size{Width: 56, Height: 56}})
			b.SetState(boolState(pal == iconPrefs.AppIcon))
			appTabView.AddSubview(b)
			iconButtons = append(iconButtons, b)
		}
		for i := range iconButtons {
			i := i
			action.Set(iconButtons[i], func(sender objc.Object) {
				pal := appIconPalettes[i]
				for j, b := range iconButtons {
					b.SetState(boolState(j == i)) // radio behavior
				}
				iconPrefs.AppIcon = pal
				_ = saveMenuPrefs(menuPrefsPath(home), iconPrefs)
				applyAppIcon(pal)
			})
		}

		// --- Tray glyph pickers --------------------------------------------
		trayHdr := appkit.TextField_LabelWithString("Menu-bar icon (tinted by state)")
		trayHdr.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: trayHdrY}, Size: foundation.Size{Width: 360, Height: 16}})
		trayHdr.SetFont(appkit.Font_BoldSystemFontOfSize(11))
		appTabView.AddSubview(trayHdr)

		mkGlyphPopup := func(labelText, current string, y float64, apply func(string)) {
			lbl := appkit.TextField_LabelWithString(labelText)
			lbl.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: y + 2}, Size: foundation.Size{Width: 90, Height: 18}})
			appTabView.AddSubview(lbl)
			pop := appkit.NewPopUpButton()
			pop.SetFrame(foundation.Rect{Origin: foundation.Point{X: 104, Y: y}, Size: foundation.Size{Width: 160, Height: 26}})
			for _, g := range trayGlyphs {
				pop.AddItemWithTitle(g)
			}
			pop.SelectItemWithTitle(current)
			action.Set(pop, func(sender objc.Object) {
				apply(pop.TitleOfSelectedItem())
			})
			appTabView.AddSubview(pop)
		}
		mkGlyphPopup("Claude:", iconPrefs.TrayClaudeGlyph, yClaude, func(g string) {
			iconPrefs.TrayClaudeGlyph = g
			_ = saveMenuPrefs(menuPrefsPath(home), iconPrefs)
			clearIconCacheAndRefresh(envPath)
		})
		mkGlyphPopup("Codex:", iconPrefs.TrayCodexGlyph, yCodex, func(g string) {
			iconPrefs.TrayCodexGlyph = g
			_ = saveMenuPrefs(menuPrefsPath(home), iconPrefs)
			clearIconCacheAndRefresh(envPath)
		})
		mkGlyphPopup("Idle:", iconPrefs.TrayIdleGlyph, yIdle, func(g string) {
			iconPrefs.TrayIdleGlyph = g
			_ = saveMenuPrefs(menuPrefsPath(home), iconPrefs)
			clearIconCacheAndRefresh(envPath)
		})

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

		// Re-render the preview whenever any element toggles. Number-slot and
		// tool checkboxes focus the rotation on the card they control so the
		// change shows immediately; glass and bottom bar appear on every card,
		// so they just re-render in place.
		focus := func(card int) func(objc.Object) {
			return func(sender objc.Object) { pw.focusCard(card) }
		}
		action.Set(ctxNumCheck, focus(cardCtx))
		action.Set(rateCheck, focus(cardRate))
		action.Set(resetCheck, focus(cardReset))
		action.Set(activityCheck, focus(cardTool))
		action.Set(trailCheck, focus(cardTool))
		action.Set(ctxCheck, func(sender objc.Object) { pw.onFormChanged() })
		action.Set(rateBarCheck, func(sender objc.Object) { pw.onFormChanged() })

		// --- Footer: Cancel / Save ----------------------------------------
		// A general error line (e.g. write failure) above the buttons.
		generalErr := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 16, Y: 34}, Size: foundation.Size{Width: winW - 32, Height: 14}})
		content.AddSubview(generalErr)

		cancelBtn := appkit.NewButtonWithTitle("Cancel")
		cancelBtn.SetBezelStyle(appkit.BezelStyleRounded)
		cancelBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 200, Y: 8}, Size: foundation.Size{Width: 88, Height: 28}})
		action.Set(cancelBtn, func(sender objc.Object) {
			w.Close() // Close() (not PerformClose) avoids sending windowShouldClose:
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
			w.Close() // Close() (not PerformClose) avoids sending windowShouldClose:
		})
		content.AddSubview(saveBtn)

		// --- Window delegate: revert to Accessory + rearm guard on close. --
		wd := &appkit.WindowDelegate{}
		wd.SetWindowWillClose(func(notification foundation.Notification) {
			groupClosed.Store(true)
			pw.stop()
			appkit.Application_SharedApplication().SetActivationPolicy(appkit.ApplicationActivationPolicyAccessory)
			settingsWindow = appkit.Window{}
			settingsTabView = appkit.TabView{}
		})
		w.SetDelegate(wd)
		settingsDelegate = wd // retain past this closure (weak delegate ref)

		// Build the Pomodoro tab into its content view, then select the
		// requested tab.
		buildPomodoroTab(w, pomoTabView, envPath)
		tabView.SelectTabViewItemAtIndex(tab)

		// Seed the preview from /state (sample fallback) and start the
		// number-slot rotation before the window becomes visible.
		pw.base, pw.live = fetchBaseSession(urlField.StringValue(), 2*time.Second)
		pw.render()
		pw.startRotation()

		// Apply the chosen dock icon now the app is .regular (window visible).
		applyAppIcon(iconPrefs.AppIcon)

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

// thumbScale is the matrix-pixel -> screen-pixel block size for the per-element
// option thumbnails. Each thumbnail is a full 32×8 display (64×16 px at 2x) so
// the element's placement on the panel is visible.
const thumbScale = 2

// thumbRegion is a matrix sub-rectangle (cols [x0,x1) × rows [y0,y1)) isolating
// one display element — see internal/render layout (numStart=12, glass cols
// 25–30, bar row 7). The thumbnail keeps only this region lit.
type thumbRegion struct{ x0, y0, x1, y1 int }

var (
	// numberRegion is the rotating number slot: 3 digits at cols 12–24, rows 1–5.
	numberRegion = thumbRegion{12, 1, 25, 6}
	// glassRegion is the context-window glass at cols 25–30, rows 1–5.
	glassRegion = thumbRegion{25, 1, 31, 6}
	// barRegion is the bottom bar at row 7, cols 11–31.
	barRegion = thumbRegion{11, 7, 32, 8}
)

// confirmUninstall shows a modal yes/no and returns true if the user confirms.
func confirmUninstall(name string) bool {
	alert := appkit.NewAlert()
	alert.SetMessageText(fmt.Sprintf("Uninstall %s?", name))
	alert.SetInformativeText("This removes its LaunchAgent (and, for the Claude producer, its hooks) and stops it now.")
	alert.AddButtonWithTitle("Uninstall")
	alert.AddButtonWithTitle("Cancel")
	return alert.RunModal() == appkit.AlertFirstButtonReturn
}

// elementThumb composes a sample frame via mut, masks it to element region r
// (dropping the robot and every other element), and renders the full 32×8
// display — a dark panel showing just that element in its true position.
func elementThumb(mut func(s *render.Session), r thumbRegion) appkit.Image {
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
	masked := render.MaskFrameToRegion(frame, r.x0, r.y0, r.x1, r.y1)
	pix, w, h := render.RenderRGBA(masked, thumbScale)
	return rgbaToImage(pix, w, h)
}
