package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

func runVersion() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Printf("awtrix-menu (build info unavailable, %s)\n", runtime.Version())
		return
	}
	rev, modified := "unknown", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	dirty := ""
	if modified {
		dirty = "+dirty"
	}
	fmt.Printf("awtrix-menu %s%s (%s)\n", rev, dirty, runtime.Version())
}
