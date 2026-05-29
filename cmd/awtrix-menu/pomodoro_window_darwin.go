//go:build darwin

package main

import (
	"fmt"
	"strconv"

	"github.com/progrium/darwinkit/helper/action"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// openPomodoroWindow opens the unified settings window on the Pomodoro tab.
func openPomodoroWindow(envPath string) { openSettingsWindowOnTab(envPath, tabPomodoro) }

// buildPomodoroTab populates the Pomodoro tab's content view: durations, rounds,
// behaviour toggles, colours, Start/Pause/Stop controls, and Cancel/Save (which
// PUTs the settings to the server). Coordinates are anchored to the top of a
// ~winH-tall tab content view (matching the Status tab's coordinate space).
func buildPomodoroTab(w appkit.Window, parent appkit.View, envPath string) {
	const (
		winW   = 460.0
		winH   = 744.0
		rowH   = 24.0
		fieldX = 180.0
		numW   = 70.0
	)

	client := pomoClientFromEnv(envPath)
	cfg, cfgErr := client.GetConfig()
	if cfgErr != nil {
		// Usable defaults so the tab still works offline.
		cfg = pomoConfig{FocusMinutes: 25, ShortBreakMinutes: 5, LongBreakMinutes: 15, RoundsBeforeLongBreak: 4, Sound: true, FocusColor: "#FF3B30", BreakColor: "#2EE85E"}
	}

	numField := func(p appkit.View, y float64, val int) appkit.TextField {
		f := appkit.NewTextField()
		f.SetStringValue(strconv.Itoa(val))
		f.SetFrame(foundation.Rect{Origin: foundation.Point{X: fieldX, Y: y}, Size: foundation.Size{Width: numW, Height: rowH}})
		p.AddSubview(f)
		return f
	}

	// --- Durations box -----------------------------------------------------
	durBox := appkit.NewBox()
	durBox.SetTitle("Durations (minutes)")
	durBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 546}, Size: foundation.Size{Width: winW - 32, Height: 168}})
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
	parent.AddSubview(durBox)

	// --- Behaviour box -----------------------------------------------------
	behBox := appkit.NewBox()
	behBox.SetTitle("Behaviour")
	behBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 438}, Size: foundation.Size{Width: winW - 32, Height: 96}})
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
	parent.AddSubview(behBox)

	// --- Colours box -------------------------------------------------------
	colBox := appkit.NewBox()
	colBox.SetTitle("Colours")
	colBox.SetFrame(foundation.Rect{Origin: foundation.Point{X: 16, Y: 330}, Size: foundation.Size{Width: winW - 32, Height: 96}})
	colView := appkit.NewView()
	colBox.SetContentView(colView)
	mkColor := func(y float64, label, hex string) appkit.TextField {
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
		return f
	}
	focusColorField := mkColor(50, "Focus colour", cfg.FocusColor)
	breakColorField := mkColor(16, "Break colour", cfg.BreakColor)
	parent.AddSubview(colBox)

	// --- Error line + Start/Pause/Stop controls ---------------------------
	errLabel := newErrorLabel(foundation.Rect{Origin: foundation.Point{X: 16, Y: 300}, Size: foundation.Size{Width: winW - 32, Height: 14}})
	parent.AddSubview(errLabel)
	if cfgErr != nil {
		errLabel.SetStringValue("Couldn't reach server — showing defaults. Check Server URL/token on the Status tab.")
	}

	ctlBtn := func(title string, x float64, verb string) {
		b := appkit.NewButtonWithTitle(title)
		b.SetBezelStyle(appkit.BezelStyleRounded)
		b.SetFrame(foundation.Rect{Origin: foundation.Point{X: x, Y: 262}, Size: foundation.Size{Width: 88, Height: 28}})
		action.Set(b, func(sender objc.Object) {
			if err := pomoClientFromEnv(envPath).Action(verb); err != nil {
				errLabel.SetStringValue(fmt.Sprintf("%s failed: %v", verb, err))
			} else {
				errLabel.SetStringValue("")
			}
		})
		parent.AddSubview(b)
	}
	ctlBtn("Start", 16, "start")
	ctlBtn("Pause", 112, "pause")
	ctlBtn("Stop", 208, "stop")

	// --- Footer: Cancel / Save --------------------------------------------
	cancelBtn := appkit.NewButtonWithTitle("Cancel")
	cancelBtn.SetBezelStyle(appkit.BezelStyleRounded)
	cancelBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 200, Y: 16}, Size: foundation.Size{Width: 88, Height: 28}})
	action.Set(cancelBtn, func(sender objc.Object) { w.PerformClose(nil) })
	parent.AddSubview(cancelBtn)

	saveBtn := appkit.NewButtonWithTitle("Save Pomodoro")
	saveBtn.SetBezelStyle(appkit.BezelStyleRounded)
	saveBtn.SetFrame(foundation.Rect{Origin: foundation.Point{X: winW - 108, Y: 16}, Size: foundation.Size{Width: 100, Height: 28}})
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
		errLabel.SetStringValue("Saved ✓")
	})
	parent.AddSubview(saveBtn)
}
