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
	case "doctor":
		runDoctor()
	case "version", "-v", "--version":
		fmt.Println("awtrix-codex-producer", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `awtrix-codex-producer

Usage:
  awtrix-codex-producer [run]      # daemon: tail Codex rollout files, POST status (default)
  awtrix-codex-producer install    # install + start the LaunchAgent
  awtrix-codex-producer uninstall  # stop + remove the LaunchAgent
  awtrix-codex-producer doctor     # show config + reachability
  awtrix-codex-producer version    # print version
  awtrix-codex-producer help       # this help

Configuration:
  ~/.config/awtrix-ai-status/producer.env (shared with the Claude producer)`)
}
