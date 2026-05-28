//go:build darwin

package main

import (
	"fmt"
	"strconv"

	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/helper/action"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// pomodoroWindow is the single reused Pomodoro settings window instance.
var pomodoroWindow appkit.Window

// openPomodoroWindow opens (or refocuses) the Pomodoro settings window. Safe to
// call from any goroutine. It loads current settings from the server, lets the
// user edit durations/colours/toggles (PUT on Save), and offers Start/Pause/Stop.
func openPomodoroWindow(envPath string) {
	dispatch.MainQueue().DispatchAsync(func() {
		app := appkit.Application_SharedApplication()
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
		appkit.RunningApplication_CurrentApplication().ActivateWithOptions(appkit.ApplicationActivateAllWindows)

		if !pomodoroWindow.IsNil() {
			pomodoroWindow.MakeKeyAndOrderFront(nil)
			return
		}

		client := pomoClientFromEnv(envPath)
		cfg, cfgErr := client.GetConfig()
		if cfgErr != nil {
			// Fall back to sensible defaults so the window is still usable.
			cfg = pomoConfig{FocusMinutes: 25, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4, Sound: true, FocusColor: "#FF3B30", BreakColor: "#2EE85E"}
		}

		const winW, winH = 420.0, 500.0
		w := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			foundation.Rect{Size: foundation.Size{Width: winW, Height: winH}},
			appkit.WindowStyleMaskTitled|appkit.WindowStyleMaskClosable,
			appkit.BackingStoreBuffered, false,
		)
		w.SetTitle("Pomodoro Settings")
		w.SetReleasedWhenClosed(false)
		content := w.ContentView()

		const (
			rowH   = 24.0
			fieldX = 180.0
			numW   = 70.0
		)
		numField := func(parent appkit.View, y float64, val int) appkit.TextField {
			f := appkit.NewTextField()
			f.SetStringValue(strconv.Itoa(val))
			f.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: y}, Size: foundation.Size{Width: numW, Height: rowH}})
			parent.AddSubview(f)
			return f
		}

		// --- Durations box -------------------------------------------------
		durBox := appkit.NewBox()
		durBox.SetTitle("Durations (minutes)")
		durBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 300}, Size: foundation.Size{Width: winW - 32, Height: 168}})
		durView := appkit.NewView()
		durBox.SetContentView(durView)

		durView.AddSubview(newFieldLabel("Focus", foundation.Point{X: 8, Y: 122}))
		focusField := numField(durView, 120, cfg.FocusMinutes)
		durView.AddSubview(newFieldLabel("Short break", foundation.Point{X: 8, Y: 90}))
		shortField := numField(durView, 88, cfg.ShortBreakMinutes)
		durView.AddSubview(newFieldLabel("Long break", foundation.Point{X: 8, Y: 58}))
		longField := numField(durView, 56, cfg.LongBreakMinutes)
		durView.AddSubview(newFieldLabel("Rounds → long", foundation.Point{X: 8, Y: 26}))
		roundsField := numField(durView, 24, cfg.RoundsBeforeLongBreak)
		content.AddSubview(durBox)

		// --- Behaviour box -------------------------------------------------
		behBox := appkit.NewBox()
		behBox.SetTitle("Behaviour")
		behBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 196}, Size: foundation.Size{Width: winW - 32, Height: 96}})
		behView := appkit.NewView()
		behBox.SetContentView(behView)

		autoCheck := appkit.NewButton()
		autoCheck.SetButtonType(appkit.ButtonTypeSwitch)
		autoCheck.SetTitle("Auto-start next phase")
		autoCheck.SetState(boolState(cfg.AutoStartNext))
		autoCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 50}, Size: foundation.Size{Width: 240, Height: rowH}})
		behView.AddSubview(autoCheck)

		soundCheck := appkit.NewButton()
		soundCheck.SetButtonType(appkit.ButtonTypeSwitch)
		soundCheck.SetTitle("Play chime at phase end")
		soundCheck.SetState(boolState(cfg.Sound))
		soundCheck.SetFrame(foundation.Rect{Origin: foundation.Point{X: 8, Y: 18}, Size: foundation.Size{Width: 240, Height: rowH}})
		behView.AddSubview(soundCheck)
		content.AddSubview(behBox)

		// --- Colours box ---------------------------------------------------
		colBox := appkit.NewBox()
		colBox.SetTitle("Colours")
		colBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 92}, Size: foundation.Size{Width: winW - 32, Height: 96}})
		colView := appkit.NewView()
		colBox.SetContentView(colView)

		mkColor := func(y float64, label, hex string) (appkit.TextField, appkit.ColorWell) {
			colView.AddSubview(newFieldLabel(label, foundation.Point{X: 8, Y: y + 2}))
			f := appkit.NewTextField()
			f.SetStringValue(hex)
			f.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: y}, Size: foundation.Size{Width: 100, Height: rowH}})
			well := appkit.NewColorWell()
			well.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX + 112, Y: y - 2}, Size: foundation.Size{Width: 44, Height: rowH + 4}})
			if c, ok := parseHexColor(hex); ok {
				well.SetColor(c)
			}
			action.Set(well, func(sender objc.Object) { f.SetStringValue(colorToHex(well.Color())) })
			colView.AddSubview(f)
			colView.AddSubview(well)
			return f, well
		}
		focusColorField, _ := mkColor(50, "Focus colour", cfg.FocusColor)
		breakColorField, _ := mkColor(16, "Break colour", cfg.BreakColor)
		content.AddSubview(colBox)

		// --- Controls + error line ----------------------------------------
		errLabel := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 16, Y: 64}, Size: foundation.Size{Width: winW - 32, Height: 14}})
		content.AddSubview(errLabel)
		if cfgErr != nil {
			errLabel.SetStringValue("Couldn't reach server — showing defaults. Check Server URL/token.")
		}

		ctlBtn := func(title string, x float64, verb string) {
			b := appkit.NewButtonWithTitle(title)
			b.SetBezelStyle(appkit.BezelStyleRounded)
			b.SetFrame(foundation.Rect{Origin: foundation.Point{X: x, Y: 44}, Size: foundation.Size{Width: 88, Height: 28}})
			action.Set(b, func(sender objc.Object) {
				if err := pomoClientFromEnv(envPath).Action(verb); err != nil {
					errLabel.SetStringValue(fmt.Sprintf("%s failed: %v", verb, err))
				} else {
					errLabel.SetStringValue("")
				}
			})
			content.AddSubview(b)
		}
		ctlBtn("Start", 16, "start")
		ctlBtn("Pause", 112, "pause")
		ctlBtn("Stop", 208, "stop")

		// --- Footer: Cancel / Save ----------------------------------------
		cancelBtn := appkit.NewButtonWithTitle("Cancel")
		cancelBtn.SetBezelStyle(appkit.BezelStyleRounded)
		cancelBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 200, Y: 8}, Size: foundation.Size{Width: 88, Height: 28}})
		action.Set(cancelBtn, func(sender objc.Object) { w.PerformClose(nil) })
		content.AddSubview(cancelBtn)

		saveBtn := appkit.NewButtonWithTitle("Save")
		saveBtn.SetBezelStyle(appkit.BezelStyleRounded)
		saveBtn.SetKeyEquivalent("\r")
		saveBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 104, Y: 8}, Size: foundation.Size{Width: 88, Height: 28}})
		action.Set(saveBtn, func(sender objc.Object) {
			w.EndEditingFor(nil)
			next, err := buildPomoConfig(
				focusField.StringValue(), shortField.StringValue(), longField.StringValue(), roundsField.StringValue(),
				autoCheck.State() == appkit.ControlStateValueOn, soundCheck.State() == appkit.ControlStateValueOn,
				focusColorField.StringValue(), breakColorField.StringValue(), cfg.SoundMelody,
			)
			if err != nil {
				errLabel.SetStringValue(err.Error())
				return
			}
			if err := pomoClientFromEnv(envPath).PutConfig(next); err != nil {
				errLabel.SetStringValue(fmt.Sprintf("could not save: %v", err))
				return
			}
			w.PerformClose(nil)
		})
		content.AddSubview(saveBtn)

		wd := &appkit.WindowDelegate{}
		wd.SetWindowWillClose(func(notification foundation.Notification) {
			appkit.Application_SharedApplication().SetActivationPolicy(appkit.ApplicationActivationPolicyAccessory)
			pomodoroWindow = appkit.Window{}
		})
		w.SetDelegate(wd)

		pomodoroWindow = w
		w.Center()
		w.MakeKeyAndOrderFront(nil)
	})
}
