package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func runUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot find home dir:", err)
		os.Exit(1)
	}
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)
	_ = exec.Command("launchctl", "bootout", target).Run()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "uninstall: remove plist:", err)
	}
	fmt.Println("Uninstall complete. producer.env was left in place (shared with the Claude producer).")
}
