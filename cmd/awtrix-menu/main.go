package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args
	cmd := "run"
	if len(args) >= 2 {
		cmd = args[1]
	}
	switch cmd {
	case "run", "":
		runApp()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "doctor":
		runDoctor()
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `awtrix-menu

Usage:
  awtrix-menu run        # default; the LaunchAgent invokes this
  awtrix-menu install    # plist + launchctl bootstrap
  awtrix-menu uninstall  # reverse install
  awtrix-menu doctor     # show config + state health
  awtrix-menu help       # this help`)
}

// stubs for later tasks
func runApp()       { fmt.Fprintln(os.Stderr, "run: not yet implemented"); os.Exit(2) }
func runInstall()   { fmt.Fprintln(os.Stderr, "install: not yet implemented"); os.Exit(2) }
func runUninstall() { fmt.Fprintln(os.Stderr, "uninstall: not yet implemented"); os.Exit(2) }
func runDoctor()    { fmt.Fprintln(os.Stderr, "doctor: not yet implemented"); os.Exit(2) }
