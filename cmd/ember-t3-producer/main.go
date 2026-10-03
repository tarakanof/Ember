// Command ember-t3-producer reports T3 Code (t3.codes) thread status to an
// Ember server. It polls T3's local SQLite state read-only; see
// docs/ARCHITECTURE.md (Producers) for why not the WebSocket RPC API.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	sub := "run"
	if len(os.Args) >= 2 {
		sub = os.Args[1]
	}
	switch sub {
	case "run":
		runDaemon()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "configure":
		runConfigure()
	case "deconfigure":
		fmt.Println("Deconfigure complete (nothing to undo).")
	case "doctor":
		runDoctor()
	case "version", "-v", "--version":
		fmt.Println("ember-t3-producer", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `ember-t3-producer

Usage:
  ember-t3-producer [run]       # daemon: poll T3 Code thread state, POST status (default)
  ember-t3-producer install     # install + start the LaunchAgent (com.ember.t3)
  ember-t3-producer uninstall   # stop + remove the LaunchAgent
  ember-t3-producer configure   # file-only setup (no LaunchAgent)
  ember-t3-producer deconfigure # reverse configure
  ember-t3-producer doctor      # show config, T3 state + reachability
  ember-t3-producer version     # print version
  ember-t3-producer help        # this help

Configuration:
  ~/.config/ember/producer.env (shared with the Claude/Codex producers)
  EMBER_T3_HOME (default ~/.t3), EMBER_T3_POLL_INTERVAL_MS, EMBER_T3_ACTIVITY_WINDOW_SECONDS`)
}
