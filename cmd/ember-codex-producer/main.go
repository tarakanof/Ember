package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	sub, args := "run", []string(nil)
	if len(os.Args) >= 2 {
		sub, args = os.Args[1], os.Args[2:]
	}
	switch sub {
	case "run":
		runDaemon()
	case "install":
		runInstall(args)
	case "uninstall":
		runUninstall()
	case "configure":
		runConfigure(args)
	case "deconfigure":
		fmt.Println("Deconfigure complete (nothing to undo: configure only seeds the shared producer.env).")
	case "doctor":
		runDoctor()
	case "discover":
		runDiscover()
	case "version", "-v", "--version":
		fmt.Println("ember-codex-producer", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `ember-codex-producer

Usage:
  ember-codex-producer [run]      # daemon: tail Codex rollouts + watch the app-server daemon, POST status (default)
  ember-codex-producer install [--headless]  # install + start the service (LaunchAgent / systemd --user)
  ember-codex-producer uninstall  # stop + remove the service
  ember-codex-producer configure  # file-only setup (no service)
  ember-codex-producer deconfigure # reverse configure
  ember-codex-producer doctor     # show config + reachability
  ember-codex-producer discover   # find the server over mDNS, cache + probe it
  ember-codex-producer version    # print version
  ember-codex-producer help       # this help

Configuration:
  ~/.config/ember/producer.env (shared with the Claude producer)
  EMBER_SERVER_URL empty or "auto" finds the server over mDNS (_ember._tcp).
  EMBER_CODEX_APPSERVER=0 stops watching the Codex app-server daemon socket.
  --headless: no Ember.app on this Mac (auto-detected; always so on Linux).`)
}
