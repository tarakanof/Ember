//go:build darwin

package main

import "github.com/progrium/darwinkit/macos/appkit"

// applyAppIcon sets the macOS app/dock icon to the chosen palette's embedded
// PNG. The dock icon is only visible while the app is .regular (settings window
// open); calling this when Accessory is harmless. Must run on the main thread.
func applyAppIcon(palette string) {
	b, err := appIconPNG(palette)
	if err != nil {
		return
	}
	img := appkit.NewImageWithData(b)
	if img.IsNil() {
		return
	}
	appkit.Application_SharedApplication().SetApplicationIconImage(img)
}
