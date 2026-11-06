package producer

import (
	"os"
	"path/filepath"
	"runtime"
)

const HeadlessFlag = "--headless"

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

func Headless(args []string, home string) bool {
	return HasFlag(args, HeadlessFlag) || !EmberAppInstalled(home)
}

func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
