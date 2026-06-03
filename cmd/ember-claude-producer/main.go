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
	fmt.Fprintln(os.Stderr, `ember-claude-producer

Usage:
  ember-claude-producer hook <event-name>     # called by Claude Code hooks
  ember-claude-producer run                   # long-lived heartbeat daemon (LaunchAgent)
  ember-claude-producer tick                  # one-shot heartbeat pass (manual/doctor)
  ember-claude-producer statusline             # called by Claude Code statusLine
  ember-claude-producer install               # one-shot setup
  ember-claude-producer uninstall             # reverse install
  ember-claude-producer doctor                # show config + state health
  ember-claude-producer help                  # this help

Configuration:
  ~/.config/ember/producer.env`)
}
