package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "hook":
		runHook(os.Args[2:])
	case "tick":
		runTick()
	case "run":
		runDaemon()
	case "statusline":
		runStatusline()
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
	fmt.Fprintln(os.Stderr, `awtrix-claude-producer

Usage:
  awtrix-claude-producer hook <event-name>     # called by Claude Code hooks
  awtrix-claude-producer run                   # long-lived heartbeat daemon (LaunchAgent)
  awtrix-claude-producer tick                  # one-shot heartbeat pass (manual/doctor)
  awtrix-claude-producer statusline             # called by Claude Code statusLine
  awtrix-claude-producer install               # one-shot setup
  awtrix-claude-producer uninstall             # reverse install
  awtrix-claude-producer doctor                # show config + state health
  awtrix-claude-producer help                  # this help

Configuration:
  ~/.config/awtrix-ai-status/producer.env`)
}
