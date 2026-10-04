package main

import (
	"fmt"
	"os"
)

var version = "dev"

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
		runInstall(os.Args[2:])
	case "uninstall":
		runUninstall()
	case "configure":
		runConfigure(os.Args[2:])
	case "deconfigure":
		runDeconfigure()
	case "doctor":
		runDoctor()
	case "version", "-v", "--version":
		fmt.Println("ember-claude-producer", version)
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
  ember-claude-producer run                   # long-lived heartbeat daemon (LaunchAgent / systemd --user)
  ember-claude-producer tick                  # one-shot heartbeat pass (manual/doctor)
  ember-claude-producer statusline             # called by Claude Code statusLine
  ember-claude-producer install [--headless]  # one-shot setup: hooks, statusline, heartbeat service
  ember-claude-producer uninstall             # reverse install
  ember-claude-producer configure             # file-only setup (no service)
  ember-claude-producer deconfigure           # reverse configure
  ember-claude-producer doctor                # show config + state health
  ember-claude-producer version               # print version
  ember-claude-producer help                  # this help

Configuration:
  ~/.config/ember/producer.env
  EMBER_SERVER_URL empty or "auto" finds the server over mDNS (_ember._tcp).
  --headless: no Ember.app on this Mac (auto-detected; always so on Linux).`)
}
