package producer

import (
	"os"
	"path/filepath"
	"runtime"
)

// HeadlessFlag is the install/configure flag forcing headless mode.
const HeadlessFlag = "--headless"

// EmberAppInstalled reports whether Ember.app sits in /Applications or
// ~/Applications; always false off macOS.
func EmberAppInstalled(home string) bool {
	return emberAppInstalledFor(runtime.GOOS, home, "/Applications")
}

func emberAppInstalledFor(goos, home, systemApps string) bool {
	if goos != "darwin" {
		return false
	}
	for _, dir := range []string{systemApps, filepath.Join(home, "Applications")} {
		if fi, err := os.Stat(filepath.Join(dir, "Ember.app")); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Headless reports whether the producer runs without Ember.app: always on
// Linux, on macOS when --headless is passed or no Ember.app is installed. In
// headless mode the CLI owns the background service and nothing points at
// the app's Settings › Agents.
func Headless(args []string, home string) bool {
	return HasFlag(args, HeadlessFlag) || !EmberAppInstalled(home)
}

// HasFlag reports whether args contains flag.
func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
