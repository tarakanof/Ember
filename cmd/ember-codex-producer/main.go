package main

import (
	"fmt"
	"os"
)

// version is overridable at build time via -ldflags "-X main.version=...".
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
		runDeconfigure()
	case "doctor":
		runDoctor()
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
  ember-codex-producer [run]      # daemon: tail Codex rollout files, POST status (default)
  ember-codex-producer install    # install + start the LaunchAgent
  ember-codex-producer uninstall  # stop + remove the LaunchAgent
  ember-codex-producer configure  # file-only setup (no LaunchAgent)
  ember-codex-producer deconfigure # reverse configure
  ember-codex-producer doctor     # show config + reachability
  ember-codex-producer version    # print version
  ember-codex-producer help       # this help

Configuration:
  ~/.config/ember/producer.env (shared with the Claude producer)`)
}
