//go:build darwin

package main

import (
	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
)

// openSettingsWindow opens the settings window. Safe to call from any
// goroutine — it marshals all AppKit work onto the main thread.
func openSettingsWindow(envPath string) {
	dispatch.MainQueue().DispatchAsync(func() {
		app := appkit.Application_SharedApplication()
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
		// NSApplication.activateIgnoringOtherApps is NOT bound in DarwinKit;
		// use the running-application route to bring this bundle-less binary
		// forward so its window can become key.
		appkit.RunningApplication_CurrentApplication().ActivateWithOptions(appkit.ApplicationActivateAllWindows)

		w := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			foundation.Rect{Size: foundation.Size{Width: 360, Height: 140}},
			appkit.WindowStyleMaskTitled|appkit.WindowStyleMaskClosable,
			appkit.BackingStoreBuffered, false,
		)
		w.SetTitle("AWTRIX Settings (spike)")

		text := appkit.NewTextField()
		text.SetFrame(foundation.Rect{Origin: foundation.Point{X: 20, Y: 80}, Size: foundation.Size{Width: 320, Height: 24}})
		secure := appkit.NewSecureTextField()
		secure.SetFrame(foundation.Rect{Origin: foundation.Point{X: 20, Y: 44}, Size: foundation.Size{Width: 320, Height: 24}})

		content := w.ContentView()
		content.AddSubview(text)
		content.AddSubview(secure)

		w.SetReleasedWhenClosed(false)
		w.MakeKeyAndOrderFront(nil)
	})
}
